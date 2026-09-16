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
- **For full coverage the floor is higher: airom ≥ v0.3.5.** On 2026-09-16 this overlay
  held 69 packs, 60 of them byte-identical copies of airom's built-ins kept in step by
  hand — the shadow fork the governance note below warns about. Those 60 were deleted
  and the other 9 promoted. Every deleted pack had been built in since v0.3.5 or earlier
  (46 of them since v0.1.0), so a bundle user on v0.3.5+ lost nothing, while one on
  v0.1.9–v0.3.4 now receives 12 fewer packs from the channel. The overlay is a staging
  channel again and no longer carries the base vocabulary for old binaries; upgrading is
  what does that.
- **After promoting, delete the promoted packs only once an airom release ships them.**
  Until then they are the bundle's entire payload, and deleting early takes that
  detection away from every user until the release lands.
- **Trust.** airom verifies the bundle signature against a public key **embedded in the
  airom binary**. The private half is this repo's `AIROM_RULES_SIGNING_KEY` secret and
  exists nowhere else. **Rotating the key means generating a new keypair, embedding the new
  public key in airom, and shipping an airom release** — coordinate it there
  (`internal/rulesync/airom-rules.pub`), not here.
- **Governance.** Stable, broadly-useful rules should be **promoted upstream** into airom's
  embedded packs (`rules/` in the airom repo) on airom's release cadence, then **deleted
  from this overlay**, so this repo stays a fast-moving staging channel rather than a
  shadow fork of the built-ins.

## Layout

```
rules/<category>/<provider>.yaml       # packs (shipped in the bundle)
rules/<category>/testdata/<provider>/  # fixtures (lint/test only; excluded from the bundle)
tools/bundle/                          # deterministic tar + manifest + ed25519 sign
.github/workflows/lint.yml             # airom rules lint/test on every pack, per PR
.github/workflows/release.yml          # build + sign + attach on a v* tag
```
