package watch

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
	"github.com/kirilligum/codex-langfuse-tracer/internal/providers"
)

type ExportFunc func(context.Context, agenttrace.Turn) (int, error)

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

func InitializeState(statePath string, now time.Time, stdout io.Writer, quiet bool) (exportstate.State, error) {
	if now.IsZero() {
		now = time.Now()
	}
	state := exportstate.State{
		Version:         1,
		ScanWatermarkNS: now.Add(-time.Duration(buildinfo.DefaultInitialLookbackSecs) * time.Second).UnixNano(),
	}
	if err := exportstate.Save(statePath, state); err != nil {
		return exportstate.State{}, err
	}
	if !quiet {
		fmt.Fprintln(writerOrDiscard(stdout), "initialized watch state; historical turns before the initial watermark will not be exported")
	}
	return state, nil
}

func ScanOnce(ctx context.Context, opts ScanOptions, state exportstate.State) (exportstate.State, int, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	stdout := writerOrDiscard(opts.Stdout)
	stderr := writerOrDiscard(opts.Stderr)
	scanStartedNS := opts.Now.UnixNano()
	watermark := state.ScanWatermarkNS
	exportedCount := 0
	exportFailed := false

	var queueExported int
	var err error
	state, queueExported, err = drainQueue(ctx, opts, state)
	if err != nil {
		return state, queueExported, err
	}
	exportedCount += queueExported

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
		for _, turn := range agenttrace.ExportableTurns(turns) {
			endNS := parseNS(agenttrace.ISOToNS(turn.EndTS))
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
				if err := exportstate.Save(opts.StatePath, state); err != nil {
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
			if err := exportstate.Save(opts.StatePath, state); err != nil {
				return state, exportedCount, err
			}
		}
	}
	return state, exportedCount, nil
}

func drainQueue(ctx context.Context, opts ScanOptions, state exportstate.State) (exportstate.State, int, error) {
	if len(state.Queue) == 0 {
		return state, 0, nil
	}
	stdout := writerOrDiscard(opts.Stdout)
	stderr := writerOrDiscard(opts.Stderr)
	exportedCount := 0
	for _, request := range append([]exportstate.QueueRequest(nil), state.Queue...) {
		turns, err := parseQueuedTurns(request)
		if err != nil {
			if !opts.Quiet {
				fmt.Fprintf(stderr, "ERROR: failed to parse queued provider=%s path=%s: %v\n", request.Provider, request.SourcePath, err)
			}
			continue
		}
		requestExported := false
		for _, turn := range agenttrace.ExportableTurns(turns) {
			if state.HasProcessed(turn.TraceID) {
				requestExported = true
				continue
			}
			if opts.Export == nil {
				fmt.Fprintf(stderr, "ERROR: failed to export provider=%s path=%s: missing export callback\n", request.Provider, request.SourcePath)
				continue
			}
			status, err := opts.Export(ctx, turn)
			if err != nil {
				fmt.Fprintf(stderr, "ERROR: failed to export provider=%s trace=%s path=%s: %v\n", request.Provider, turn.TraceID, request.SourcePath, err)
				continue
			}
			state.AddProcessed(turn.TraceID)
			exportedCount++
			requestExported = true
			if !opts.Quiet {
				fmt.Fprintf(stdout, "exported provider=%s trace=%s status=%d path=%s\n", request.Provider, turn.TraceID, status, request.SourcePath)
			}
		}
		if requestExported {
			state.RemoveQueued(request)
			if opts.StatePath != "" {
				if err := exportstate.Save(opts.StatePath, state); err != nil {
					return state, exportedCount, err
				}
			}
		}
	}
	return state, exportedCount, nil
}

func parseQueuedTurns(request exportstate.QueueRequest) ([]agenttrace.Turn, error) {
	return providers.ParseTurns(request.Provider, request.SourcePath)
}

func WatchSessions(ctx context.Context, opts ScanOptions) error {
	state, err := exportstate.Load(opts.StatePath)
	if err != nil {
		return err
	}
	current := exportstate.State{}
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
		if opts.StatePath != "" {
			latest, err := exportstate.Load(opts.StatePath)
			if err != nil {
				return err
			}
			if latest != nil {
				current = *latest
			}
		}
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
