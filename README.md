# airom-rules

The overlay rule-pack channel for [**airom**](https://github.com/airomhq/airom),
the open-source AI Bill of Materials scanner. Rules here ship to users as a
**signed bundle** that airom fetches on demand — so detection for a new AI
framework can land without waiting for a new airom binary.

```bash
airom rules update        # fetch + verify + cache the latest bundle
airom fs .                # scans now use it (see airom's docs)
```

## How it fits together

airom embeds these packs as its **offline floor** — it always works with no
network. `airom rules update` fetches a newer bundle from this repo's releases,
verifies it, and caches it; scans then **layer that bundle over** the embedded
packs, merged by rule ID, with `--rules` overlays on top of both. A scan itself
never touches the network.

Layering is what makes a bundle safe to be partial: a pack this repo does not
carry falls through to airom's built-in one, so shipping nine packs does not
retract the other sixty.

- **This repo is the source of truth for rule content.** New/experimental/long-tail
  framework rules land here first and reach users via `rules update`.
- **Stable rules get promoted upstream** into airom's embedded packs on airom's
  own release cadence, and can then be deleted here.
- **One caveat before deleting: airom ≤ v0.4.5 used the bundle *instead of* the
  built-ins**, so for those users a pack missing here is a pack that does not
  exist. Deleting a promoted pack retracts it from anyone still on that line. See
  MAINTAINING.md, "The airom coupling", for what that cost on 2026-09-16.

## Repo layout

```
rules/<category>/<provider>.yaml      # one pack per provider
rules/<category>/testdata/<provider>/ # ≥1 positive + ≥1 negative fixture per rule
eol/<provider>.yaml                   # model lifecycle catalogs (retirement dates)
tools/bundle/                         # builds + signs the release bundle
```

Categories mirror airom: `models`, `embeddings`, `frameworks`, `vectordb`,
`infra`, `params`, `prompts`, `datasets`, `security`. `eol/` is not a category —
it is a sibling namespace with its own schema, which airom's rule loader skips
and its lifecycle loader reads.

## Model lifecycle catalogs

`eol/<provider>.yaml` carries the retirement facts behind AIROM's lifecycle
overlay: per model, what the provider announced, when, and the URL it was
transcribed from. They ship in the same signed bundle as the rule packs, which
is the point — **retirement dates change on a provider's calendar, not on
AIROM's release schedule**, so `airom rules update` refreshes them without a new
binary.

Two things to know before editing one:

- **The overlay is per provider, and within a provider it wins entirely.** A
  bundle carrying `eol/openai.yaml` replaces airom's built-in OpenAI records and
  leaves Anthropic's alone. So a record dropped here IS dropped for bundle users
  — that is the unit of intent, and it is why the file has to stay complete
  rather than carrying only what changed.
- **This repo is where a catalog is edited first.** airom keeps a copy compiled
  in as its offline floor, refreshed from here at release time. Editing airom's
  copy alone reaches only users who never run `rules update`; editing this one
  reaches everyone else within a day.

Every record is transcribed from the provider's own deprecation page, never
inferred from a naming pattern, and carries the date a maintainer last checked
it. A model absent from the file gets **no** lifecycle claim — not a quiet
"supported".

## Adding a rule pack

1. Scaffold it with airom (guarantees the schema + fixture layout):
   ```bash
   airom dev new-rulepack <provider>   # in an airom checkout, then copy here
   ```
   …or copy an existing pack and edit. See airom's `docs/rule-schema.md`.
2. Every rule needs **non-empty `keywords`** (they gate the Aho–Corasick
   prefilter), a **globally unique `id`** as `<provider>/<rule-name>`, a
   compiling **RE2** regex, and a `regions:` list (never matches comments).
3. Every rule needs **≥1 positive and ≥1 negative fixture** under
   `testdata/<provider>/`.
4. Validate with airom's own tools (CI runs exactly these):
   ```bash
   airom rules lint rules/<category>/<provider>.yaml
   airom rules test rules/<category>/<provider>.yaml
   ```

## Releasing a bundle

Push a `vX.Y.Z` tag. CI builds a deterministic gzipped tar of the packs, writes
a `manifest.json` (version, tarball SHA-256, counts), signs the manifest with the
repo's ed25519 key (`AIROM_RULES_SIGNING_KEY` secret), and attaches all three to
the GitHub release. airom verifies the signature against the public key embedded
in its binary, then the tarball checksum, then extracts — any failure is fatal.

Build one locally (unsigned) to inspect:

```bash
go run ./tools/bundle -rules rules -version v0.0.0 -out dist -unsigned
```

## Consuming it in CI

Pin both the tool and the rules for reproducibility:

```bash
pip install airom==<version>
airom rules update <bundle-version>     # or omit for latest
airom fs . -o cyclonedx=aibom.json      # the AIBOM records rulesVersion + rulesHash
```

`--offline` refuses to fetch but still scans the last cached bundle;
`--no-cached-rules` forces airom's embedded packs.

## Trust

The signing key's private half lives only in this repo's `AIROM_RULES_SIGNING_KEY`
CI secret; its public half is embedded in airom. Rotating it means generating a
new keypair and shipping an airom release with the new public key.

## License

Apache-2.0 (same as airom). See [LICENSE](LICENSE).
