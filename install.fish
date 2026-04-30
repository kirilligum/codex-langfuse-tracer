#!/usr/bin/env fish

set -l repo_dir (dirname (status --current-filename))
set -l codex_home "$HOME/.codex"
if set -q CODEX_HOME
    set codex_home $CODEX_HOME
end

set -l exporter_src "$repo_dir/bin/export_codex_session_to_langfuse.py"
set -l wrapper_src "$repo_dir/fish/functions/codex.fish"
set -l exporter_dst "$codex_home/bin/export_codex_session_to_langfuse.py"
set -l wrapper_dst "$HOME/.config/fish/functions/codex.fish"

if not test -f $exporter_src
    echo "missing $exporter_src" >&2
    exit 1
end

if not test -f $wrapper_src
    echo "missing $wrapper_src" >&2
    exit 1
end

mkdir -p (dirname $exporter_dst)
mkdir -p (dirname $wrapper_dst)

install -m 755 $exporter_src $exporter_dst

if test -f $wrapper_dst
    if not cmp -s $wrapper_src $wrapper_dst
        set -l backup "$wrapper_dst.backup."(date +%Y%m%d%H%M%S)
        cp $wrapper_dst $backup
        echo "backed up existing fish codex function to $backup"
    end
end

install -m 644 $wrapper_src $wrapper_dst

echo "installed exporter: $exporter_dst"
echo "installed fish wrapper: $wrapper_dst"
echo "reload fish with: functions -e codex; source $wrapper_dst"
echo "configure Langfuse credentials and Codex OTEL in ~/.codex/config.toml before expecting traces."
