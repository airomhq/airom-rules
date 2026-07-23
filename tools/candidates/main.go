// Command candidates reports AI frameworks that show up in a corpus of real
// repositories but are NOT yet covered by an airom-rules pack — a data-driven
// "what to add next", so frameworks are prioritized by real usage rather than
// picked by hand.
//
// Method: a repo counts as an "AI repo" if it declares a dependency we already
// cover (an anchor). In those repos, every uncovered dependency that isn't
// common infrastructure becomes a candidate, ranked by how many distinct AI
// repos use it. Coverage comes from the packs themselves (pack:/provider:/id).
//
// Sources: requirements*.txt, package.json, pyproject.toml. Stdlib-only.
//
// Usage:
//
//	go run ./tools/candidates -corpus ~/ai-corpus            # each subdir = a repo
//	go run ./tools/candidates -corpus ~/ai-corpus -top 60 -min 2
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

func main() {
	corpus := flag.String("corpus", "", "directory whose immediate subdirectories are cloned repos (required)")
	rulesDir := flag.String("rules", "rules", "airom-rules pack directory (for coverage)")
	top := flag.Int("top", 40, "show at most N candidates")
	min := flag.Int("min", 2, "only show candidates used by >= this many AI repos")
	flag.Parse()
	if *corpus == "" {
		fatal("-corpus is required (a directory of cloned repos)")
	}
	if err := run(*corpus, *rulesDir, *top, *min); err != nil {
		fatal(err.Error())
	}
}

func run(corpus, rulesDir string, top, min int) error {
	covered, err := loadCoverage(rulesDir)
	if err != nil {
		return err
	}
	repos, err := listRepos(corpus)
	if err != nil {
		return err
	}

	type stat struct {
		display  string
		aiRepos  int
		total    int
		examples []string
	}
	cands := map[string]*stat{} // key: squashed name
	aiRepoCount := 0

	for _, repo := range repos {
		deps := collectRepoDeps(repo.path)
		if len(deps) == 0 {
			continue
		}
		// An AI repo declares at least one thing we already cover.
		isAI := false
		for sq := range deps {
			if covered[sq] {
				isAI = true
				break
			}
		}
		if !isAI {
			continue
		}
		aiRepoCount++
		for sq, disp := range deps {
			if covered[sq] || stoplist[sq] || len(sq) < 2 {
				continue
			}
			s := cands[sq]
			if s == nil {
				s = &stat{display: disp}
				cands[sq] = s
			}
			s.aiRepos++
			s.total++ // one repo counts once (deps is a set per repo)
			if len(s.examples) < 3 {
				s.examples = append(s.examples, repo.name)
			}
		}
	}

	ranked := make([]*stat, 0, len(cands))
	for _, s := range cands {
		if s.aiRepos >= min {
			ranked = append(ranked, s)
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].aiRepos != ranked[j].aiRepos {
			return ranked[i].aiRepos > ranked[j].aiRepos
		}
		return ranked[i].display < ranked[j].display
	})

	fmt.Printf("Corpus:   %s\n", corpus)
	fmt.Printf("Repos:    %d scanned, %d are AI repos (declare a covered dependency)\n", len(repos), aiRepoCount)
	fmt.Printf("Coverage: %d covered framework tokens across the packs\n\n", len(covered))
	if len(ranked) == 0 {
		fmt.Println("No uncovered AI-adjacent candidates met the threshold.")
		fmt.Println("Try a broader corpus, or lower -min.")
		return nil
	}
	fmt.Printf("Uncovered AI-adjacent candidates (>= %d AI repos), ranked:\n\n", min)
	fmt.Printf("  %-4s %-28s %-9s %s\n", "#", "CANDIDATE", "AI-REPOS", "EXAMPLE REPOS")
	for i, s := range ranked {
		if i >= top {
			fmt.Printf("\n  … and %d more below the top %d.\n", len(ranked)-top, top)
			break
		}
		fmt.Printf("  %-4d %-28s %-9d %s\n", i+1, s.display, s.aiRepos, strings.Join(s.examples, ", "))
	}
	fmt.Printf("\nEach is used alongside AI you already detect but has no pack. Review the top:\n")
	fmt.Printf("skip obvious non-frameworks and family variants (e.g. langchain-core). Coverage\n")
	fmt.Printf("is name-based, so a candidate can be hidden by an unrelated pack of the same name\n")
	fmt.Printf("(e.g. `instructor` the embedding model vs the LLM library) — spot-check names you\n")
	fmt.Printf("expect to see. Then scaffold with `airom dev new-rulepack`.\n")
	return nil
}

// ── coverage ─────────────────────────────────────────────────────────────────

var coverField = regexp.MustCompile(`(?m)^\s*(?:pack|provider):\s*["']?([A-Za-z0-9][A-Za-z0-9._-]*)`)
var idField = regexp.MustCompile(`(?m)^\s*-\s+id:\s*["']?([A-Za-z0-9][A-Za-z0-9._-]*)/`)

// loadCoverage builds the set of framework tokens the packs already cover, from
// each pack's pack:/provider: values and rule id prefixes (all squashed).
func loadCoverage(rulesDir string) (map[string]bool, error) {
	covered := map[string]bool{}
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
		if !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range coverField.FindAllSubmatch(data, -1) {
			covered[squash(string(m[1]))] = true
		}
		for _, m := range idField.FindAllSubmatch(data, -1) {
			covered[squash(string(m[1]))] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read coverage from %s: %w", rulesDir, err)
	}
	if len(covered) == 0 {
		return nil, fmt.Errorf("no coverage found under %s (is it the rules dir?)", rulesDir)
	}
	return covered, nil
}

// ── corpus ───────────────────────────────────────────────────────────────────

type repo struct {
	name string
	path string
}

// listRepos returns each immediate subdirectory of corpus as a repo. If corpus
// has no subdirectories, it is treated as a single repo.
func listRepos(corpus string) ([]repo, error) {
	entries, err := os.ReadDir(corpus)
	if err != nil {
		return nil, err
	}
	var repos []repo
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			repos = append(repos, repo{name: e.Name(), path: filepath.Join(corpus, e.Name())})
		}
	}
	if len(repos) == 0 {
		repos = append(repos, repo{name: filepath.Base(corpus), path: corpus})
	}
	return repos, nil
}

var skipDir = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".venv": true,
	"venv": true, "__pycache__": true, "dist": true, "build": true, ".tox": true,
}

const maxManifest = 2 << 20 // 2 MiB

// collectRepoDeps walks a repo and returns its declared dependencies as
// squashed-name -> display-name (a set; each package counted once per repo).
func collectRepoDeps(root string) map[string]string {
	deps := map[string]string{}
	add := func(names []string) {
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			sq := squash(n)
			if sq == "" {
				continue
			}
			if _, ok := deps[sq]; !ok {
				deps[sq] = strings.ToLower(n)
			}
		}
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep going
		}
		if d.IsDir() {
			if skipDir[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		isReq := strings.HasPrefix(name, "requirements") && strings.HasSuffix(name, ".txt")
		if !isReq && name != "package.json" && name != "pyproject.toml" {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxManifest {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		switch {
		case isReq:
			add(parseRequirements(data))
		case name == "package.json":
			add(parsePackageJSON(data))
		case name == "pyproject.toml":
			add(parsePyproject(data))
		}
		return nil
	})
	return deps
}

// parseRequirements extracts package names from a requirements.txt, dropping
// comments, flags, URLs/VCS refs, and version/extras/marker suffixes.
func parseRequirements(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		if strings.Contains(line, "://") {
			continue // URL or VCS install
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		if n := leadingName(line); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func parsePackageJSON(data []byte) []string {
	var pkg struct {
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
		PeerDeps        map[string]json.RawMessage `json:"peerDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	var out []string
	for _, m := range []map[string]json.RawMessage{pkg.Dependencies, pkg.DevDependencies, pkg.PeerDeps} {
		for k := range m {
			out = append(out, k)
		}
	}
	return out
}

var quotedReq = regexp.MustCompile(`["']([A-Za-z0-9][A-Za-z0-9._-]*)`)

// parsePyproject is a best-effort reader for PEP 621 `dependencies = [...]` and
// Poetry `[tool.poetry.dependencies]` tables — no TOML dependency.
func parsePyproject(data []byte) []string {
	var out []string
	inArray, inPoetry := false, false
	for _, ln := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "[") {
			inPoetry = strings.Contains(t, "poetry") && strings.Contains(t, "dependencies")
			inArray = false
			continue
		}
		if !inArray && strings.HasPrefix(t, "dependencies") && strings.Contains(t, "[") {
			inArray = true
		}
		if inArray {
			for _, m := range quotedReq.FindAllStringSubmatch(t, -1) {
				out = append(out, m[1])
			}
			if strings.Contains(t, "]") {
				inArray = false
			}
			continue
		}
		if inPoetry {
			if i := strings.Index(t, "="); i > 0 {
				name := strings.Trim(strings.TrimSpace(t[:i]), `"'`)
				if name != "" && name != "python" && leadingName(name) == name {
					out = append(out, name)
				}
			}
		}
	}
	return out
}

// ── helpers ──────────────────────────────────────────────────────────────────

// leadingName returns the leading package-name token of a requirement string:
// "langchain[all]>=0.2" -> "langchain".
func leadingName(s string) string {
	for i, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-') {
			return s[:i]
		}
	}
	return s
}

// squash normalizes a package name for matching: lowercase, separators removed,
// so llama-index / llama_index / llamaindex all collapse to one token.
func squash(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r == '-' || r == '_' || r == '.' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// stoplist is common non-AI infrastructure to exclude from candidates (squashed).
// Edit as the corpus surfaces new noise; over-listing only hides a candidate,
// never marks it covered.
var stoplist = toSet(
	// Python infra
	"numpy", "pandas", "scipy", "requests", "httpx", "aiohttp", "urllib3",
	"pydantic", "pydanticsettings", "fastapi", "starlette", "flask", "django",
	"uvicorn", "gunicorn", "sqlalchemy", "alembic", "redis", "celery", "boto3",
	"botocore", "pyyaml", "python-dotenv", "dotenv", "jinja2", "click", "typer",
	"rich", "tqdm", "pytest", "tox", "black", "ruff", "mypy", "isort", "flake8",
	"setuptools", "wheel", "pip", "matplotlib", "seaborn", "pillow", "orjson",
	"ujson", "websockets", "typingextensions", "packaging", "protobuf", "grpcio",
	"tenacity", "loguru", "structlog", "attrs", "cachetools", "certifi",
	// Node infra
	"react", "reactdom", "vue", "svelte", "next", "express", "lodash", "axios",
	"typescript", "eslint", "prettier", "jest", "vitest", "webpack", "vite",
	"rollup", "tailwindcss", "zod", "dotenvcli",
)

func toSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[squash(s)] = true
	}
	return m
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "candidates: "+msg)
	os.Exit(1)
}
