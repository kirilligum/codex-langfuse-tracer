#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
codex_home="${CODEX_HOME:-$HOME/.codex}"
systemd_user_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

exporter_dst="$codex_home/bin/codex-langfuse-exporter"
service_src="$repo_dir/systemd/codex-langfuse-watch.service"
service_dst="$systemd_user_dir/codex-langfuse-watch.service"
service_name="codex-langfuse-watch.service"

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
staged_dir=""
staged_service_dir=""
staged_exporter=""
staged_service=""
cutover_started=0
service_stopped=0
cleanup() {
    status=$?
    trap - EXIT
    if [ -n "$staged_dir" ]; then
        rm -rf -- "$staged_dir"
    fi
    if [ -n "$staged_service_dir" ]; then
        rm -rf -- "$staged_service_dir"
    fi
    if [ "$status" -ne 0 ] && [ "$cutover_started" -eq 1 ]; then
        service_state="$(systemctl --user is-active "$service_name" 2>/dev/null || true)"
        if [ "$service_stopped" -eq 1 ]; then
            echo "install failed after stopping the watcher; current service state: ${service_state:-unknown}" >&2
        else
            echo "install failed during fresh service promotion; current service state: ${service_state:-unknown}" >&2
        fi
        echo "review the error above, then rerun ./install.sh to finish installation and restart the watcher" >&2
    fi
    exit "$status"
}
trap cleanup EXIT

staged_dir="$(mktemp -d "$exporter_dst.stage.XXXXXX")"
staged_exporter="$staged_dir/codex-langfuse-exporter"
staged_service_dir="$(mktemp -d "$service_dst.stage.XXXXXX")"
staged_service="$staged_service_dir/codex-langfuse-watch.service"

(cd "$repo_dir" && go build -o "$staged_exporter" ./cmd/codex-langfuse-exporter)
install -m 644 "$service_src" "$staged_service"
"$staged_exporter" --sync-model-pricing --quiet

load_state="$(systemctl --user show --property=LoadState --value "$service_name")"
if [ -z "$load_state" ]; then
    echo "systemd returned an empty load state for $service_name; refusing to replace the installed exporter" >&2
    exit 1
fi
if [ "$load_state" != "not-found" ]; then
    systemctl --user stop "$service_name"
    service_stopped=1
fi

cutover_started=1
mv -f -- "$staged_exporter" "$exporter_dst"
mv -f -- "$staged_service" "$service_dst"

systemctl --user daemon-reload
systemctl --user enable "$service_name"
systemctl --user restart "$service_name"

echo "installed exporter: $exporter_dst"
echo "installed service: $service_dst"
echo "restarted service: $service_name"
echo "synced Langfuse model pricing from ~/.codex/config.toml"
