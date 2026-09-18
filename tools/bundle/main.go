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
	"errors"
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
	// Informational, like the two counts above. Safe to add: airom parses the
	// manifest with a plain json.Unmarshal, so an older client ignores it, and
	// the signature covers the bytes either way.
	CatalogCount int `json:"catalogCount,omitempty"`
}

// entry is one file destined for the tarball: where it goes, where it comes
// from, and whether its "- id:" lines are rules. Catalogs share that syntax
// without being rules.
type entry struct {
	tarName string
	srcPath string
	isRule  bool
}

const tarballName = "airom-rules.tar.gz"

var ruleLine = regexp.MustCompile(`(?m)^\s*-\s+id:\s`)

// collectCatalogs returns the model lifecycle catalogs under eolDir, destined
// for eol/ in the tarball — the path internal/eol.LoadBundle walks, and one
// that internal/ruleengine skips when loading rule packs. A missing directory
// is not an error: a bundle may legitimately carry no lifecycle data.
func collectCatalogs(eolDir string) ([]entry, error) {
	if eolDir == "" {
		return nil, nil
	}
	if _, err := os.Stat(eolDir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var out []entry
	err := filepath.WalkDir(eolDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, err := filepath.Rel(eolDir, p)
		if err != nil {
			return err
		}
		out = append(out, entry{tarName: "eol/" + filepath.ToSlash(rel), srcPath: p})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].tarName < out[j].tarName })
	return out, nil
}

func main() {
	rulesDir := flag.String("rules", "rules", "directory of rule packs")
	eolDir := flag.String("eol", "eol", "directory of model lifecycle catalogs (packed under eol/)")
	version := flag.String("version", "", "release version, e.g. v1.2.0 (required)")
	outDir := flag.String("out", "dist", "output directory for the bundle assets")
	unsigned := flag.Bool("unsigned", false, "skip signing (no .sig produced)")
	flag.Parse()
	if *version == "" {
		fatal("-version is required (e.g. v1.2.0)")
	}
	// An explicit -eol must produce catalogs. The release workflow passes it, so
	// a renamed or emptied directory fails the release instead of quietly
	// shipping a bundle with no lifecycle data — which is indistinguishable, to
	// every client, from a bundle that never carried any.
	eolExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "eol" {
			eolExplicit = true
		}
	})
	if err := run(*rulesDir, *eolDir, eolExplicit, *version, *outDir, *unsigned); err != nil {
		fatal(err.Error())
	}
}

func run(rulesDir, eolDir string, eolExplicit bool, version, outDir string, unsigned bool) error {
	packs, err := collectPacks(rulesDir)
	if err != nil {
		return err
	}
	if len(packs) == 0 {
		return fmt.Errorf("no rule packs found under %s", rulesDir)
	}
	entries := make([]entry, 0, len(packs))
	for _, rel := range packs {
		entries = append(entries, entry{tarName: rel, srcPath: filepath.Join(rulesDir, filepath.FromSlash(rel)), isRule: true})
	}

	catalogs, err := collectCatalogs(eolDir)
	if err != nil {
		return err
	}
	// eolDir == "" is the opt-out the error below names, so it must not trip it.
	if len(catalogs) == 0 && eolExplicit && eolDir != "" {
		return fmt.Errorf("-eol %s holds no .yaml catalogs; pass -eol \"\" to publish a bundle without lifecycle data", eolDir)
	}
	entries = append(entries, catalogs...)

	tarball, rules, err := buildTarball(entries)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(tarball)
	mf := manifest{
		Version:      version,
		Tarball:      tarballName,
		SHA256:       hex.EncodeToString(sum[:]),
		RuleCount:    rules,
		PackCount:    len(packs),
		CatalogCount: len(catalogs),
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

	fmt.Printf("bundle %s: %d pack(s), %d rule(s), %d lifecycle catalog(s), sha256 %s\n",
		version, mf.PackCount, mf.RuleCount, mf.CatalogCount, mf.SHA256)
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
func buildTarball(entries []entry) ([]byte, int, error) {
	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	gz.ModTime = time.Time{} // deterministic gzip header
	tw := tar.NewWriter(gz)

	sort.Slice(entries, func(i, j int) bool { return entries[i].tarName < entries[j].tarName })

	rules := 0
	for _, e := range entries {
		data, err := os.ReadFile(e.srcPath)
		if err != nil {
			return nil, 0, err
		}
		// Only rule packs. A catalog's "- id: claude-opus-5" matches ruleLine
		// too, so counting it here would report 90 rules for 16.
		if e.isRule {
			rules += len(ruleLine.FindAllIndex(data, -1))
		}
		hdr := &tar.Header{
			Name:     e.tarName,
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
