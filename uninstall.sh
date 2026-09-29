#!/usr/bin/env bash
set -euo pipefail

codex_home="${CODEX_HOME:-$HOME/.codex}"
systemd_user_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

exporter_dst="$codex_home/bin/codex-langfuse-exporter"
log_file="$codex_home/langfuse-transcript-export.log"
state_file="$codex_home/langfuse-export-state.json"
service_dst="$systemd_user_dir/codex-langfuse-watch.service"

systemctl --user disable --now codex-langfuse-watch.service >/dev/null 2>&1 || true
rm -f "$exporter_dst"
rm -f "$log_file"
rm -f "$state_file"
rm -f "$service_dst"
systemctl --user daemon-reload >/dev/null 2>&1 || true

echo "removed service: $service_dst"
echo "removed exporter: $exporter_dst"
echo "removed state: $state_file"
echo "Codex MCP settings were not changed; keep any independent Langfuse MCP configuration used by other workflows."
