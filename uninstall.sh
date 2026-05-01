#!/usr/bin/env bash
set -euo pipefail

codex_home="${CODEX_HOME:-$HOME/.codex}"
systemd_user_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

exporter_dst="$codex_home/bin/export_codex_session_to_langfuse.py"
log_file="$codex_home/langfuse-transcript-export.log"
state_file="$codex_home/langfuse-export-state.json"
old_wrapper_dst="$codex_home/bin/codex"
service_dst="$systemd_user_dir/codex-langfuse-watch.service"

systemctl --user disable --now codex-langfuse-watch.service >/dev/null 2>&1 || true
rm -f "$exporter_dst"
rm -f "$log_file"
rm -f "$state_file"
rm -f "$old_wrapper_dst"
rm -f "$service_dst"
systemctl --user daemon-reload >/dev/null 2>&1 || true

echo "removed service: $service_dst"
echo "removed exporter: $exporter_dst"
echo "removed state: $state_file"
echo "edit ~/.codex/config.toml to remove the optional [mcp_servers.langfuse] block."
