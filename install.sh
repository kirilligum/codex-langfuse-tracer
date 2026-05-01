#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
codex_home="${CODEX_HOME:-$HOME/.codex}"

exporter_src="$repo_dir/bin/export_codex_session_to_langfuse.py"
wrapper_src="$repo_dir/shell/functions/codex.sh"
exporter_dst="$codex_home/bin/export_codex_session_to_langfuse.py"
wrapper_dst="$codex_home/shell/codex-langfuse-tracer.sh"

if [ ! -f "$exporter_src" ]; then
    echo "missing $exporter_src" >&2
    exit 1
fi

if [ ! -f "$wrapper_src" ]; then
    echo "missing $wrapper_src" >&2
    exit 1
fi

mkdir -p "$(dirname "$exporter_dst")"
mkdir -p "$(dirname "$wrapper_dst")"

install -m 755 "$exporter_src" "$exporter_dst"
install -m 644 "$wrapper_src" "$wrapper_dst"

echo "installed exporter: $exporter_dst"
echo "installed bash/zsh wrapper: $wrapper_dst"
echo "load it with: source $wrapper_dst"
echo "configure Langfuse credentials and Codex OTEL in ~/.codex/config.toml before expecting traces."
