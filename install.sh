#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
codex_home="${CODEX_HOME:-$HOME/.codex}"
systemd_user_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

exporter_dst="$codex_home/bin/codex-langfuse-exporter"
service_src="$repo_dir/systemd/codex-langfuse-watch.service"
service_dst="$systemd_user_dir/codex-langfuse-watch.service"

if [ ! -d "$repo_dir/cmd/codex-langfuse-exporter" ]; then
    echo "missing $repo_dir/cmd/codex-langfuse-exporter" >&2
    exit 1
fi

if [ ! -f "$service_src" ]; then
    echo "missing $service_src" >&2
    exit 1
fi

mkdir -p "$(dirname "$exporter_dst")"
mkdir -p "$systemd_user_dir"
(cd "$repo_dir" && go build -o "$exporter_dst" ./cmd/codex-langfuse-exporter)
"$exporter_dst" --sync-model-pricing --quiet
install -m 644 "$service_src" "$service_dst"

systemctl --user daemon-reload
systemctl --user enable codex-langfuse-watch.service
systemctl --user restart codex-langfuse-watch.service

echo "installed exporter: $exporter_dst"
echo "installed service: $service_dst"
echo "restarted service: codex-langfuse-watch.service"
echo "synced Langfuse model pricing from ~/.codex/config.toml"
