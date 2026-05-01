#!/usr/bin/env bash
set -euo pipefail

codex_home="${CODEX_HOME:-$HOME/.codex}"
wrapper_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
exporter="$codex_home/bin/export_codex_session_to_langfuse.py"
log_file="$codex_home/langfuse-transcript-export.log"

is_exec=0
resume_session_id=""
for arg in "$@"; do
    if [ "$arg" = "exec" ]; then
        is_exec=1
    fi
done
previous_arg=""
for arg in "$@"; do
    if [ "$previous_arg" = "resume" ] && [ "${arg#-}" = "$arg" ]; then
        resume_session_id="$arg"
        break
    fi
    previous_arg="$arg"
done

real_codex=""
IFS=:
for path_dir in $PATH; do
    if [ "$path_dir" = "$wrapper_dir" ]; then
        continue
    fi
    candidate="$path_dir/codex"
    if [ -x "$candidate" ]; then
        real_codex="$candidate"
        break
    fi
done
unset IFS

if [ -z "$real_codex" ]; then
    printf '%s\n' "error: real codex binary not found on PATH after excluding $wrapper_dir" >&2
    exit 127
fi

marker="$(mktemp "${TMPDIR:-/tmp}/codex-langfuse.XXXXXX")"
output_log="$(mktemp "${TMPDIR:-/tmp}/codex-langfuse-output.XXXXXX")"
started_at="$(date '+%Y-%m-%dT%H-%M-%S')"

set +e
if [ "$is_exec" -eq 1 ]; then
    "$real_codex" "$@" 2>&1 | tee "$output_log"
    codex_status=${PIPESTATUS[0]}
else
    "$real_codex" "$@"
    codex_status=$?
fi
set -e

if [ -x "$exporter" ]; then
    selected_session=""
    if [ "$is_exec" -eq 1 ]; then
        session_id="$(sed -n 's/^session id: //p' "$output_log" | tail -n 1 | tr -d '\r')"
        if [ -n "$session_id" ]; then
            for _ in 1 2 3 4 5 6 7 8 9 10; do
                selected_session="$(find "$codex_home/sessions" -name "*$session_id*.jsonl" -print -quit 2>/dev/null)"
                if [ -n "$selected_session" ]; then
                    break
                fi
                sleep 0.5
            done
        fi
    elif [ -n "$resume_session_id" ]; then
        for _ in 1 2 3 4 5 6 7 8 9 10; do
            selected_session="$(find "$codex_home/sessions" -name "*$resume_session_id*.jsonl" -print -quit 2>/dev/null)"
            if [ -n "$selected_session" ]; then
                break
            fi
            sleep 0.5
        done
    else
        for _ in 1 2 3 4 5 6 7 8 9 10; do
            selected_session="$(
                find "$codex_home/sessions" -name 'rollout-*.jsonl' -printf '%f %p\n' 2>/dev/null \
                    | awk -v started_at="$started_at" '{
                        rollout_started_at = substr($1, 9, 19)
                        if (rollout_started_at >= started_at) {
                            print
                        }
                    }' \
                    | sort \
                    | head -n 1 \
                    | cut -d' ' -f2-
            )"
            if [ -n "$selected_session" ]; then
                break
            fi
            sleep 0.5
        done
    fi

    if [ -n "$selected_session" ]; then
        "$exporter" --path "$selected_session" --quiet --verify-wait-seconds 60 --verify-interval-seconds 5 >>"$log_file" 2>&1 && {
            printf 'exported_session=%s\n' "$selected_session" >>"$log_file"
        } || {
            printf '%s\n' "warning: Langfuse transcript export failed; see $log_file" >&2
        }
    else
        printf 'no_new_codex_session_after=%s\n' "$marker" >>"$log_file"
    fi
fi

rm -f "$marker" "$output_log"
exit "$codex_status"
