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

## Browser Automation

- For browser automation that should use the user's active Chrome profile, logged-in sessions, or visible browser state, use the configured Playwright MCP browser attached to `http://localhost:9222`.
- Expected Chrome launch command: `google-chrome --remote-debugging-port=9222 --user-data-dir=$HOME/.chrome-debug --no-first-run --no-default-browser-check`
- Avoid the Playwright CLI wrapper for those tasks because it may launch or use a separate browser session.
- Use a separate or clean browser only when isolation is intentional, such as reproducible UI testing, clean screenshots, or when the DevTools endpoint is unavailable.
