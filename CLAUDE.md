# airom-rules — Claude guide

This repo is the **signed rule-pack update channel** for
[airom](https://github.com/airomhq/airom) ("Model B"). airom embeds these packs
as an offline floor; `airom rules update` fetches a signed bundle from this
repo's releases and scans prefer it.

## Scope

Do rules work **here**. Do **not** modify the airom monorepo from this repo's
sessions. The one unavoidable exception is **signing-key rotation**, which edits
`internal/rulesync/airom-rules.pub` in airom and needs an airom release — call it
out explicitly before touching airom; never do it silently.

## The maintenance loop

Full detail in [MAINTAINING.md](MAINTAINING.md). In short:

1. Add/edit a pack under `rules/<category>/<provider>.yaml` and fixtures under
   `rules/<category>/testdata/<provider>/` (≥1 positive + ≥1 negative per rule).
2. Validate exactly as CI does: `make lint` (or `airom rules lint|test <pack>`).
   Requires `pip install airom`.
3. PR → the lint workflow gates it.
4. Ship to users: `git tag vX.Y.Z && git push origin vX.Y.Z` → CI builds a
   deterministic tarball, signs the manifest (ed25519), and attaches the bundle.

## Facts that aren't obvious from the code

- **Signing key:** private half lives only in the `AIROM_RULES_SIGNING_KEY` CI
  secret; public half is embedded in airom. Rotation happens in airom.
- **Versioning:** bundle tags here are independent of airom versions. Users need
  airom **≥ v0.1.9** for `rules update`.
- **Determinism:** scans never fetch; the bundle is byte-reproducible.
- **Governance:** promote stable rules upstream into airom's embedded packs over
  time, then delete them here — keep this a staging channel, not a shadow fork.

## Conventions

- Commit as `Roro1727 <rohan872784@gmail.com>`.
- `tools/bundle` is stdlib-only Go; the rest is YAML data — keep it that way.
