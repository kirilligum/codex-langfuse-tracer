#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
codex_home="${CODEX_HOME:-$HOME/.codex}"
systemd_user_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

exporter_src="$repo_dir/bin/export_codex_session_to_langfuse.py"
service_src="$repo_dir/systemd/codex-langfuse-watch.service"
exporter_dst="$codex_home/bin/export_codex_session_to_langfuse.py"
old_wrapper_dst="$codex_home/bin/codex"
service_dst="$systemd_user_dir/codex-langfuse-watch.service"

if [ ! -f "$exporter_src" ]; then
    echo "missing $exporter_src" >&2
    exit 1
fi

if [ ! -f "$service_src" ]; then
    echo "missing $service_src" >&2
    exit 1
fi

mkdir -p "$(dirname "$exporter_dst")"
mkdir -p "$systemd_user_dir"
install -m 755 "$exporter_src" "$exporter_dst"
install -m 644 "$service_src" "$service_dst"
rm -f "$old_wrapper_dst"

systemctl --user daemon-reload
systemctl --user enable --now codex-langfuse-watch.service

echo "installed exporter: $exporter_dst"
echo "installed service: $service_dst"
echo "started service: codex-langfuse-watch.service"
echo "configure Langfuse credentials in ~/.codex/config.toml before expecting traces."
