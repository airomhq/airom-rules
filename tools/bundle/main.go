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

	// CreatedAt is when this bundle was built (RFC 3339, UTC). Versions are
	// monotonic but undated, so without it a client cannot tell a bundle
	// published yesterday from one published a year ago. It sits inside the
	// signed bytes, which is what makes it an attested claim rather than a
	// number anyone in the path can rewrite.
	CreatedAt string `json:"createdAt"`

	// MinAirom is the oldest airom that can read this bundle. Clients new
	// enough to read the field refuse a bundle above their own version at
	// install, with one clear message, instead of installing something they
	// will fail to parse on every scan afterwards.
	MinAirom string `json:"minAirom,omitempty"`
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

// collectCatalogs returns the catalogs under dir, destined for ns/ in the
// tarball — the path the owning package's LoadBundle walks, and one that
// internal/ruleengine skips when loading rule packs (its NonRulePackDirs). A
// missing directory is not an error: a bundle may legitimately carry neither
// lifecycle nor exploitation data.
//
// Parameterized by namespace rather than copied per catalog: the two differ
// only in which directory they read and which prefix they write, and a copy
// would be the kind that drifts.
func collectCatalogs(ns, dir string) ([]entry, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var out []entry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, entry{tarName: ns + "/" + filepath.ToSlash(rel), srcPath: p})
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
	// Default OFF, deliberately. A bundle carrying kev/ is unreadable to every
	// airom whose rule walk does not skip that namespace: the walker parses
	// kev/cisa.yaml as a rule pack, the whole ruleset load fails, and the
	// client silently drops back to its built-in packs — the v0.1.8 failure
	// mode, with the channel's rules turned off. minAirom does not save them,
	// because the clients at risk (v0.4.6 and older) are exactly the ones that
	// predate the manifest field and ignore it.
	//
	// So publishing the catalog this way is gated on adoption, not on the code
	// being ready: pass -kev kev once v0.4.7-and-newer is the floor you are
	// willing to serve, and raise -min-airom in the same breath. Until then the
	// embedded catalog in airom is the only copy, refreshed by tools/kev-gen on
	// airom's own release cadence.
	kevDir := flag.String("kev", "", "directory of known-exploited catalogs (packed under kev/); EMPTY BY DEFAULT — see the comment, publishing one breaks older airom")
	version := flag.String("version", "", "release version, e.g. v1.2.0 (required)")
	outDir := flag.String("out", "dist", "output directory for the bundle assets")
	unsigned := flag.Bool("unsigned", false, "skip signing (no .sig produced)")
	// v0.1.9 is the oldest airom with `rules update` at all (MAINTAINING.md,
	// "The airom coupling"), so it is the floor every bundle has always had —
	// now stated in the manifest instead of only in prose. Raise it in the same
	// commit that starts using a pack feature older airom cannot parse.
	minAirom := flag.String("min-airom", "v0.1.9", "oldest airom that can read this bundle")
	flag.Parse()
	if *version == "" {
		fatal("-version is required (e.g. v1.2.0)")
	}
	// An explicit -eol must produce catalogs. The release workflow passes it, so
	// a renamed or emptied directory fails the release instead of quietly
	// shipping a bundle with no lifecycle data — which is indistinguishable, to
	// every client, from a bundle that never carried any.
	eolExplicit, kevExplicit := false, false
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "eol":
			eolExplicit = true
		case "kev":
			kevExplicit = true
		}
	})
	if err := run(*rulesDir, *eolDir, eolExplicit, *kevDir, kevExplicit, *version, *minAirom, *outDir, *unsigned); err != nil {
		fatal(err.Error())
	}
}

func run(rulesDir, eolDir string, eolExplicit bool, kevDir string, kevExplicit bool, version, minAirom, outDir string, unsigned bool) error {
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

	catalogs, err := collectCatalogs("eol", eolDir)
	if err != nil {
		return err
	}
	// eolDir == "" is the opt-out the error below names, so it must not trip it.
	if len(catalogs) == 0 && eolExplicit && eolDir != "" {
		return fmt.Errorf("-eol %s holds no .yaml catalogs; pass -eol \"\" to publish a bundle without lifecycle data", eolDir)
	}
	entries = append(entries, catalogs...)

	// The CISA known-exploited catalog travels the same way, in its own
	// namespace. It is the one catalog whose staleness is a security property
	// rather than an inconvenience: CISA adds entries several times a week, and
	// an old catalog under-reports exploitation that is already public — which
	// is why this bundle, not an airom release, is the lever that refreshes it.
	kevCatalogs, err := collectCatalogs("kev", kevDir)
	if err != nil {
		return err
	}
	if len(kevCatalogs) == 0 && kevExplicit && kevDir != "" {
		return fmt.Errorf("-kev %s holds no .yaml catalogs; pass -kev \"\" to publish a bundle without exploitation data", kevDir)
	}
	entries = append(entries, kevCatalogs...)
	catalogs = append(catalogs, kevCatalogs...)

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
		// The TARBALL stays reproducible (sorted entries, zeroed mtimes); this
		// timestamp lives in the manifest, so two builds of identical content
		// still agree on the sha256 that matters.
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		MinAirom:  minAirom,
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

	fmt.Printf("bundle %s: %d pack(s), %d rule(s), %d catalog(s), sha256 %s\n",
		version, mf.PackCount, mf.RuleCount, mf.CatalogCount, mf.SHA256)
	fmt.Printf("  built %s, needs airom %s or newer\n", mf.CreatedAt, mf.MinAirom)
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
