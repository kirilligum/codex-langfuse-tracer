#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
codex_home="${CODEX_HOME:-$HOME/.codex}"

exporter_src="$repo_dir/bin/export_codex_session_to_langfuse.py"
wrapper_src="$repo_dir/bin/codex-langfuse-wrapper.sh"
exporter_dst="$codex_home/bin/export_codex_session_to_langfuse.py"
wrapper_dst="$codex_home/bin/codex"

if [ ! -f "$exporter_src" ]; then
    echo "missing $exporter_src" >&2
    exit 1
fi

if [ ! -f "$wrapper_src" ]; then
    echo "missing $wrapper_src" >&2
    exit 1
fi

mkdir -p "$(dirname "$exporter_dst")"
install -m 755 "$exporter_src" "$exporter_dst"
install -m 755 "$wrapper_src" "$wrapper_dst"

echo "installed exporter: $exporter_dst"
echo "installed codex wrapper: $wrapper_dst"
echo "put $codex_home/bin before the real codex binary on PATH."
echo "configure Langfuse credentials in ~/.codex/config.toml before expecting traces."
