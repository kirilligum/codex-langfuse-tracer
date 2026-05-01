#!/usr/bin/env bash
set -euo pipefail

codex_home="${CODEX_HOME:-$HOME/.codex}"

exporter_dst="$codex_home/bin/export_codex_session_to_langfuse.py"
log_file="$codex_home/langfuse-transcript-export.log"
wrapper_dst="$codex_home/bin/codex"

rm -f "$exporter_dst"
rm -f "$log_file"
rm -f "$wrapper_dst"

echo "removed codex wrapper: $wrapper_dst"
echo "removed exporter: $exporter_dst"
echo "edit ~/.codex/config.toml to remove the optional [mcp_servers.langfuse] block."
