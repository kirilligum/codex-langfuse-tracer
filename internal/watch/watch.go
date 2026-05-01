package watch

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
)

type ExportFunc func(context.Context, codextrace.Turn) (int, error)

type ScanOptions struct {
	Root                string
	StatePath           string
	Now                 time.Time
	Stdout              io.Writer
	Stderr              io.Writer
	Quiet               bool
	Export              ExportFunc
	PollIntervalSeconds float64
	InitialLookbackSecs int
}

func InitializeState(statePath string, now time.Time, stdout io.Writer, quiet bool) (State, error) {
	if now.IsZero() {
		now = time.Now()
	}
	state := State{
		Version:         1,
		ScanWatermarkNS: now.Add(-time.Duration(buildinfo.DefaultInitialLookbackSecs) * time.Second).UnixNano(),
	}
	if err := SaveState(statePath, state); err != nil {
		return State{}, err
	}
	if !quiet {
		fmt.Fprintln(writerOrDiscard(stdout), "initialized watch state; historical turns before the initial watermark will not be exported")
	}
	return state, nil
}

func ScanOnce(ctx context.Context, opts ScanOptions, state State) (State, int, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	stdout := writerOrDiscard(opts.Stdout)
	stderr := writerOrDiscard(opts.Stderr)
	scanStartedNS := opts.Now.UnixNano()
	watermark := state.ScanWatermarkNS
	exportedCount := 0
	exportFailed := false

	for _, sessionPath := range codextrace.SessionPaths(opts.Root) {
		info, err := os.Stat(sessionPath)
		if err != nil {
			if !opts.Quiet {
				fmt.Fprintf(stderr, "warning: skipped unreadable rollout %s: %v\n", sessionPath, err)
			}
			continue
		}
		mtimeNS := info.ModTime().UnixNano()
		if mtimeNS <= watermark || mtimeNS > scanStartedNS {
			continue
		}

		turns, err := codextrace.ParseTurns(sessionPath)
		if err != nil {
			if !opts.Quiet {
				fmt.Fprintf(stderr, "warning: skipped unreadable rollout %s: %v\n", sessionPath, err)
			}
			continue
		}
		for _, turn := range codextrace.ExportableTurns(turns) {
			endNS := parseNS(codextrace.ISOToNS(turn.EndTS))
			if endNS <= watermark || state.HasProcessed(turn.TraceID) {
				continue
			}
			if opts.Export == nil {
				exportFailed = true
				fmt.Fprintf(stderr, "ERROR: failed to export trace=%s path=%s: missing export callback\n", turn.TraceID, sessionPath)
				continue
			}
			status, err := opts.Export(ctx, turn)
			if err != nil {
				exportFailed = true
				fmt.Fprintf(stderr, "ERROR: failed to export trace=%s path=%s: %v\n", turn.TraceID, sessionPath, err)
				continue
			}
			state.AddProcessed(turn.TraceID)
			exportedCount++
			if opts.StatePath != "" {
				if err := SaveState(opts.StatePath, state); err != nil {
					return state, exportedCount, err
				}
			}
			if !opts.Quiet {
				fmt.Fprintf(stdout, "exported trace=%s status=%d path=%s\n", turn.TraceID, status, sessionPath)
			}
		}
	}

	if !exportFailed {
		state.ScanWatermarkNS = scanStartedNS
		if opts.StatePath != "" {
			if err := SaveState(opts.StatePath, state); err != nil {
				return state, exportedCount, err
			}
		}
	}
	return state, exportedCount, nil
}

func WatchSessions(ctx context.Context, opts ScanOptions) error {
	state, err := LoadState(opts.StatePath)
	if err != nil {
		return err
	}
	current := State{}
	if state == nil {
		current, err = InitializeState(opts.StatePath, time.Now(), opts.Stdout, opts.Quiet)
		if err != nil {
			return err
		}
	} else {
		current = *state
	}
	if !opts.Quiet {
		fmt.Fprintf(writerOrDiscard(opts.Stdout), "watching %s\n", opts.Root)
	}
	interval := time.Duration(opts.PollIntervalSeconds * float64(time.Second))
	if interval < 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	for {
		var err error
		current, _, err = ScanOnce(ctx, opts, current)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func writerOrDiscard(writer io.Writer) io.Writer {
	if writer != nil {
		return writer
	}
	return io.Discard
}

func parseNS(value string) int64 {
	var result int64
	_, _ = fmt.Sscanf(value, "%d", &result)
	return result
}
