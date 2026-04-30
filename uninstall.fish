#!/usr/bin/env fish

set -l codex_home "$HOME/.codex"
if set -q CODEX_HOME
    set codex_home $CODEX_HOME
end

set -l exporter_dst "$codex_home/bin/export_codex_session_to_langfuse.py"
set -l log_file "$codex_home/langfuse-transcript-export.log"
set -l wrapper_dst "$HOME/.config/fish/functions/codex.fish"

rm -f $exporter_dst
rm -f $log_file

if test -f $wrapper_dst
    if command grep -q "codex-langfuse-tracer managed wrapper" $wrapper_dst
        rm -f $wrapper_dst
        if functions -q codex
            functions -e codex
        end
        echo "removed fish wrapper: $wrapper_dst"
    else
        echo "not removing $wrapper_dst because it does not look managed by codex-langfuse-tracer" >&2
    end
end

echo "removed exporter: $exporter_dst"
echo "edit ~/.codex/config.toml to remove [otel] and optional [mcp_servers.langfuse] blocks."
