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

type ResolveWorkspaceFunc func(context.Context, agenttrace.Turn) (agenttrace.Turn, string, error)
type ExportSpansFunc func(context.Context, agenttrace.Turn, string) (int, error)
type ExportScoresFunc func(context.Context, agenttrace.Turn, string) error

type ScanOptions struct {
	Root                string
	StatePath           string
	Now                 time.Time
	Stdout              io.Writer
	Stderr              io.Writer
	Quiet               bool
	ResolveWorkspace    ResolveWorkspaceFunc
	ExportSpans         ExportSpansFunc
	ExportScores        ExportScoresFunc
	PollIntervalSeconds float64
	InitialLookbackSecs int
}

func InitializeState(statePath string, now time.Time, stdout io.Writer, quiet bool) (exportstate.State, error) {
	if now.IsZero() {
		now = time.Now()
	}
	state := exportstate.State{
		Version:         exportstate.Version,
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
	stderr := writerOrDiscard(opts.Stderr)
	scanStartedNS := opts.Now.UnixNano()
	watermark := state.ScanWatermarkNS
	exportedCount := 0
	exportFailed := false
	attemptedExport := false

	var queueExported int
	var err error
	state, queueExported, err = drainQueue(ctx, opts, state, &attemptedExport)
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
		for _, turn := range turns {
			if state.HasProcessed(turn.TraceID) {
				continue
			}
			var emitted int
			var failed bool
			state, emitted, failed, err = processTurn(ctx, opts, state, turn, sessionPath, &attemptedExport)
			if err != nil {
				return state, exportedCount + emitted, err
			}
			exportedCount += emitted
			exportFailed = exportFailed || failed
		}
	}

	if !exportFailed {
		state, err = mutateState(opts.StatePath, state, func(current *exportstate.State) {
			current.ScanWatermarkNS = scanStartedNS
		})
		if err != nil {
			return state, exportedCount, err
		}
	}
	return state, exportedCount, nil
}

func processTurn(ctx context.Context, opts ScanOptions, state exportstate.State, turn agenttrace.Turn, sourcePath string, attemptedExport *bool) (exportstate.State, int, bool, error) {
	traceID := turn.TraceID
	environment := state.PendingScoreEnvironment(traceID)
	needsSpans := environment == ""
	if needsSpans && !isExportable(turn) {
		return state, 0, false, nil
	}

	if needsSpans {
		if opts.ResolveWorkspace == nil {
			fmt.Fprintf(writerOrDiscard(opts.Stderr), "ERROR: failed to resolve workspace trace=%s path=%s: missing workspace resolver callback\n", traceID, sourcePath)
			return state, 0, true, nil
		}
		resolvedTurn, resolvedEnvironment, err := opts.ResolveWorkspace(ctx, turn)
		if err != nil {
			return state, 0, false, err
		}
		turn = resolvedTurn
		environment = resolvedEnvironment
		if environment == "" {
			return state, 0, false, fmt.Errorf("workspace resolver returned empty environment for trace %s", traceID)
		}
		if *attemptedExport {
			if err := waitBetweenExports(ctx, opts.PollIntervalSeconds); err != nil {
				return state, 0, false, err
			}
		}
		*attemptedExport = true

		if opts.ExportSpans == nil {
			fmt.Fprintf(writerOrDiscard(opts.Stderr), "ERROR: failed to export trace=%s path=%s: missing span export callback\n", traceID, sourcePath)
			return state, 0, true, nil
		}
		status, err := opts.ExportSpans(ctx, turn, environment)
		if err != nil {
			fmt.Fprintf(writerOrDiscard(opts.Stderr), "ERROR: failed to export trace=%s path=%s: %v\n", traceID, sourcePath, err)
			return state, 0, true, nil
		}
		state, err = mutateState(opts.StatePath, state, func(current *exportstate.State) {
			current.SetPendingScore(traceID, environment)
		})
		if err != nil {
			return state, 0, false, err
		}
		if !opts.Quiet {
			fmt.Fprintf(writerOrDiscard(opts.Stdout), "exported trace=%s status=%d path=%s\n", traceID, status, sourcePath)
		}
	}

	if opts.ExportScores == nil {
		fmt.Fprintf(writerOrDiscard(opts.Stderr), "ERROR: failed to score trace=%s path=%s: missing score export callback\n", traceID, sourcePath)
		return state, boolToInt(needsSpans), true, nil
	}
	if err := opts.ExportScores(ctx, turn, environment); err != nil {
		fmt.Fprintf(writerOrDiscard(opts.Stderr), "ERROR: failed to score trace=%s path=%s: %v\n", traceID, sourcePath, err)
		return state, boolToInt(needsSpans), true, nil
	}
	state, err := mutateState(opts.StatePath, state, func(current *exportstate.State) {
		current.AddProcessed(traceID)
	})
	if err != nil {
		return state, boolToInt(needsSpans), false, err
	}
	if !opts.Quiet {
		fmt.Fprintf(writerOrDiscard(opts.Stdout), "scored trace=%s path=%s\n", traceID, sourcePath)
	}
	return state, boolToInt(needsSpans), false, nil
}

func isExportable(turn agenttrace.Turn) bool {
	return turn.Completed && turn.TraceID != "" && turn.InputText() != "" && turn.OutputText() != ""
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func drainQueue(ctx context.Context, opts ScanOptions, state exportstate.State, attemptedExport *bool) (exportstate.State, int, error) {
	if len(state.Queue) == 0 {
		return state, 0, nil
	}
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
		requestComplete := true
		hasExportableTurn := false
		for _, turn := range agenttrace.ExportableTurns(turns) {
			hasExportableTurn = true
			if state.HasProcessed(turn.TraceID) {
				continue
			}
			var emitted int
			var failed bool
			state, emitted, failed, err = processTurn(ctx, opts, state, turn, request.SourcePath, attemptedExport)
			if err != nil {
				return state, exportedCount + emitted, err
			}
			exportedCount += emitted
			if failed || !state.HasProcessed(turn.TraceID) {
				requestComplete = false
			}
		}
		if hasExportableTurn && requestComplete {
			state, err = mutateState(opts.StatePath, state, func(current *exportstate.State) {
				current.RemoveQueued(request)
			})
			if err != nil {
				return state, exportedCount, err
			}
		}
	}
	return state, exportedCount, nil
}

func mutateState(path string, state exportstate.State, mutate func(*exportstate.State)) (exportstate.State, error) {
	if path == "" {
		mutate(&state)
		return state, nil
	}
	return exportstate.Update(path, func(current *exportstate.State) error {
		mutate(current)
		return nil
	})
}

func waitBetweenExports(ctx context.Context, pollIntervalSeconds float64) error {
	interval := time.Duration(pollIntervalSeconds * float64(time.Second))
	if interval <= 0 {
		return nil
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
