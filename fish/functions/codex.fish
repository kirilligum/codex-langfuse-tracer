function codex --description 'Run Codex and export visible transcript text to Langfuse'
    # codex-langfuse-tracer managed wrapper
    set -l marker (mktemp -t codex-langfuse.XXXXXX)
    touch $marker

    set -l codex_home "$HOME/.codex"
    if set -q CODEX_HOME
        set codex_home $CODEX_HOME
    end

    set -l exporter "$codex_home/bin/export_codex_session_to_langfuse.py"
    set -l log_file "$codex_home/langfuse-transcript-export.log"
    set -l watcher_pid

    if test -x $exporter
        $exporter --watch --start-after-marker $marker --quiet --no-verify --poll-interval-seconds 2 --watch-timeout-seconds 43200 >>$log_file 2>&1 &
        set watcher_pid $last_pid
    end

    command codex $argv
    set -l codex_status $status

    if test -n "$watcher_pid"
        sleep 2
    end

    if test -x $exporter
        set -l newest_session (find "$codex_home/sessions" -name 'rollout-*.jsonl' -newer $marker 2>/dev/null | sort | tail -n 1)

        if test -n "$newest_session"
            $exporter --path $newest_session --quiet --verify-wait-seconds 60 --verify-interval-seconds 5 >>$log_file 2>&1
            if test $status -ne 0
                echo "warning: Langfuse transcript export failed; see $log_file" >&2
            end
        end
    end

    if test -n "$watcher_pid"
        command kill $watcher_pid 2>/dev/null
        wait $watcher_pid 2>/dev/null
    end

    rm -f $marker
    return $codex_status
end
