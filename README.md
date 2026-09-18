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
tools/bundle/                         # builds + signs the release bundle
```

Categories mirror airom: `models`, `embeddings`, `frameworks`, `vectordb`,
`infra`, `params`, `prompts`, `datasets`, `security`.

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
