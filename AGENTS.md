# Agent Notes

This repo is public-facing. Keep changes small, direct, and secret-free.

Use these files as the source of truth:

- `README.md`: user-facing install, configuration, usage, safety, and troubleshooting.
- `TESTING.md`: local verification commands and production gate.
- `testdata/manifest.json`: single rollout fixture inventory.
- `testdata/rollouts/` and `testdata/golden/`: normalized trace contract corpus.

Do not add a second fixture registry, wrapper export path, native Codex OTEL path, include/exclude config surface, or per-file observation fanout unless real usage proves it is necessary.

Default verification before handing off changes:

```sh
go test ./... -count=1
git diff --check
```
