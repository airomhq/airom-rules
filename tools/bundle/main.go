// Command bundle builds a signed rule-pack bundle for a release: a deterministic
// gzipped tar of the rule packs, a manifest.json describing it, and an ed25519
// signature over the manifest. airom fetches and verifies these three assets.
//
// Usage:
//
//	AIROM_RULES_SIGNING_KEY=<base64 ed25519 private key> \
//	  go run ./tools/bundle -rules rules -version v1.2.0 -out dist
//
// The signing key is the base64 of a 64-byte ed25519 private key; its public
// half is embedded in airom (internal/rulesync/airom-rules.pub). Pass
// -unsigned to skip signing (produces no .sig — for local inspection only).
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type manifest struct {
	Version   string `json:"version"`
	Tarball   string `json:"tarball"`
	SHA256    string `json:"sha256"`
	RuleCount int    `json:"ruleCount"`
	PackCount int    `json:"packCount"`
}

const tarballName = "airom-rules.tar.gz"

var ruleLine = regexp.MustCompile(`(?m)^\s*-\s+id:\s`)

func main() {
	rulesDir := flag.String("rules", "rules", "directory of rule packs")
	version := flag.String("version", "", "release version, e.g. v1.2.0 (required)")
	outDir := flag.String("out", "dist", "output directory for the bundle assets")
	unsigned := flag.Bool("unsigned", false, "skip signing (no .sig produced)")
	flag.Parse()
	if *version == "" {
		fatal("-version is required (e.g. v1.2.0)")
	}
	if err := run(*rulesDir, *version, *outDir, *unsigned); err != nil {
		fatal(err.Error())
	}
}

func run(rulesDir, version, outDir string, unsigned bool) error {
	packs, err := collectPacks(rulesDir)
	if err != nil {
		return err
	}
	if len(packs) == 0 {
		return fmt.Errorf("no rule packs found under %s", rulesDir)
	}

	tarball, rules, err := buildTarball(rulesDir, packs)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(tarball)
	mf := manifest{
		Version:   version,
		Tarball:   tarballName,
		SHA256:    hex.EncodeToString(sum[:]),
		RuleCount: rules,
		PackCount: len(packs),
	}
	manifestBytes, err := json.Marshal(mf)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, tarballName), tarball, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), manifestBytes, 0o644); err != nil {
		return err
	}

	if !unsigned {
		sig, err := sign(manifestBytes)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, "manifest.json.sig"), []byte(sig+"\n"), 0o644); err != nil {
			return err
		}
	}

	fmt.Printf("bundle %s: %d pack(s), %d rule(s), sha256 %s\n", version, mf.PackCount, mf.RuleCount, mf.SHA256)
	if unsigned {
		fmt.Println("(unsigned — for local inspection only)")
	}
	return nil
}

// collectPacks returns the sorted, slash-relative paths of every pack file
// (rules/**/*.yaml), excluding testdata fixtures.
func collectPacks(rulesDir string) ([]string, error) {
	var packs []string
	err := filepath.WalkDir(rulesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".yaml") || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, err := filepath.Rel(rulesDir, p)
		if err != nil {
			return err
		}
		packs = append(packs, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(packs)
	return packs, nil
}

// buildTarball writes a deterministic gzipped tar (sorted entries, zeroed
// timestamps) and returns it plus the total rule count.
func buildTarball(rulesDir string, packs []string) ([]byte, int, error) {
	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	gz.ModTime = time.Time{} // deterministic gzip header
	tw := tar.NewWriter(gz)

	rules := 0
	for _, rel := range packs {
		data, err := os.ReadFile(filepath.Join(rulesDir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, 0, err
		}
		rules += len(ruleLine.FindAllIndex(data, -1))
		hdr := &tar.Header{
			Name:     rel,
			Mode:     0o644,
			Size:     int64(len(data)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Time{},
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, 0, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, 0, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, 0, err
	}
	if err := gz.Close(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), rules, nil
}

// sign returns the base64 ed25519 signature over data, using the base64 private
// key in AIROM_RULES_SIGNING_KEY.
func sign(data []byte) (string, error) {
	keyB64 := strings.TrimSpace(os.Getenv("AIROM_RULES_SIGNING_KEY"))
	if keyB64 == "" {
		return "", fmt.Errorf("AIROM_RULES_SIGNING_KEY is not set (base64 ed25519 private key); pass -unsigned to skip")
	}
	raw, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return "", fmt.Errorf("AIROM_RULES_SIGNING_KEY is not valid base64: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("AIROM_RULES_SIGNING_KEY is %d bytes, want %d", len(raw), ed25519.PrivateKeySize)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(raw), data)), nil
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "bundle: "+msg)
	os.Exit(1)
}
