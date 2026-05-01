codex() {
    # codex-langfuse-tracer managed wrapper
    local marker codex_home exporter log_file newest_session codex_status

    marker="$(mktemp "${TMPDIR:-/tmp}/codex-langfuse.XXXXXX")" || return 1

    codex_home="${CODEX_HOME:-$HOME/.codex}"
    exporter="$codex_home/bin/export_codex_session_to_langfuse.py"
    log_file="$codex_home/langfuse-transcript-export.log"

    command codex "$@"
    codex_status=$?

    if [ -x "$exporter" ]; then
        newest_session="$(
            find "$codex_home/sessions" -name 'rollout-*.jsonl' -newer "$marker" 2>/dev/null | sort | tail -n 1
        )"

        if [ -n "$newest_session" ]; then
            "$exporter" --path "$newest_session" --quiet --verify-wait-seconds 60 --verify-interval-seconds 5 >>"$log_file" 2>&1
            if [ $? -ne 0 ]; then
                printf '%s\n' "warning: Langfuse transcript export failed; see $log_file" >&2
            fi
        fi
    fi

    rm -f "$marker"
    return "$codex_status"
}
