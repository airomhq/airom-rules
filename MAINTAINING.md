# Maintaining airom-rules

This repo is the **signed rule-pack channel** for [airom](https://github.com/airomhq/airom).
Everything a maintainer does lives here; you rarely need the airom monorepo.

## Prerequisites

```bash
pip install airom          # the validator + the tool users run (airom rules lint/test/update)
# Go 1.25+ only if you want to build a bundle locally (tools/bundle)
```

## Finding what to add (don't pick names by hand)

Let usage data rank the backlog. `tools/candidates` scans a corpus of real repos
and reports AI-adjacent dependencies that appear **alongside frameworks you
already cover** but have **no pack**, ranked by how many AI repos use each.

```bash
# 1. build a corpus — each immediate subdirectory is one repo
mkdir ~/ai-corpus
gh repo clone <owner>/<repo> ~/ai-corpus/<repo>   # a batch of llm/agents/rag repos

# 2. from the airom-rules root, run the gap report
go run ./tools/candidates -corpus ~/ai-corpus              # ranked candidates
go run ./tools/candidates -corpus ~/ai-corpus -min 3 -top 60
```

The top of the list is your queue. Coverage is **name-based**, so skip family
variants (`langchain-core`) and spot-check for **name collisions** — a pack named
for one concept can hide a different package of the same name (e.g. `instructor`
the embedding model vs the LLM library). Then scaffold the winner with
`airom dev new-rulepack` and follow the next section.

## Add or update a rule pack

1. **Write the pack** under `rules/<category>/<provider>.yaml`. Categories:
   `models, embeddings, frameworks, vectordb, infra, params, prompts, datasets, security`.
   A pack is `pack:` / `version:` / `rules[]`, each rule with `id` (`<provider>/<name>`,
   globally unique), `kind`, `provider`, `languages`, non-empty `keywords`, a compiling
   **RE2** `pattern`, `regions:` (`code`/`string` — never comments), a `claim`, and
   `confidence`. See airom's `docs/rule-schema.md`. Scaffold with `airom dev new-rulepack`
   in an airom checkout if you want a starting template.
2. **Add fixtures** under `rules/<category>/testdata/<provider>/` — at least one positive
   and one negative per rule. Lint fails without them.
3. **Validate** (exactly what CI runs):
   ```bash
   make lint                                   # every pack
   airom rules lint rules/frameworks/agno.yaml # or one pack
   airom rules test rules/frameworks/agno.yaml
   ```
4. Open a PR. The lint workflow gates it.

## Releases are automatic

**You don't cut releases by hand.** Merging a pack change to `main` **auto-cuts a signed
bundle release** — the version patch-bumps from the latest release, versioned
**independently of airom** so airom's product version never moves for a rules change. Users
get it with `airom rules update` (latest) or `airom rules update vX.Y.Z` (pinned).
Fixture-only edits don't trigger a release (the bundle is unchanged).

The workflow builds a deterministic tarball, writes `manifest.json`, signs it with the
repo's ed25519 key, and attaches all three to the GitHub release.

To force a specific version (e.g. a minor bump for a big batch), dispatch it:

```bash
gh workflow run release.yml -f version=v0.2.0   # blank input = auto patch-bump
```

Inspect a bundle locally without signing:

```bash
make bundle          # -> dist/ (unsigned)
```

## The airom coupling — read before releasing

- **Users need a Model-B-capable airom** (**≥ v0.1.9**). Older airom has no `rules update`.
  A new bundle reaches users who have installed one of those airom versions or newer.
- **Trust.** airom verifies the bundle signature against a public key **embedded in the
  airom binary**. The private half is this repo's `AIROM_RULES_SIGNING_KEY` secret and
  exists nowhere else. **Rotating the key means generating a new keypair, embedding the new
  public key in airom, and shipping an airom release** — coordinate it there
  (`internal/rulesync/airom-rules.pub`), not here.
- **The bundle REPLACES the embedded packs; it does not merge with them.** A scan picks
  one base layer — the cached bundle if there is one, otherwise airom's built-ins
  (`resolveRuleBase`, airom's `internal/app/rules.go`). Only `--rules` files layer on
  top. So **this repo must stay complete**: for every user who has ever run
  `airom rules update`, the bundle is the entire ruleset, and a pack missing here is a
  pack that does not exist for them, no matter which airom they run.
- **Governance, and its live caveat.** The intent is that stable rules get **promoted
  upstream** into airom's embedded packs and then **deleted here**, so this repo stays a
  staging channel rather than a shadow fork. **Do not perform the deletion half today.**
  Because of the point above, deleting a promoted pack removes it from every bundle user
  immediately. That is not theory: on 2026-09-16, 60 promoted duplicates were deleted on
  exactly this reasoning, v0.1.8 shipped 9 packs, and a code-only scan that had reported
  three components reported none. v0.1.9 restored them seven minutes later, byte-identical
  to v0.1.7.
  So **promotion means copy, not move.** The duplication between this repo and airom's
  `rules/` is load-bearing until airom layers the bundle over the built-ins (merged by
  rule ID, bundle winning, the way `--rules` already works). Promotion still earns its
  keep — it serves `--no-cached-rules`, CI and offline scans, and it subjects a pack to
  airom's catalog cross-check, which only reads embedded packs and which caught two
  provider mismatches the day those nine were promoted.

## Layout

```
rules/<category>/<provider>.yaml       # packs (shipped in the bundle)
rules/<category>/testdata/<provider>/  # fixtures (lint/test only; excluded from the bundle)
tools/bundle/                          # deterministic tar + manifest + ed25519 sign
.github/workflows/lint.yml             # airom rules lint/test on every pack, per PR
.github/workflows/release.yml          # build + sign + attach on a v* tag
```
