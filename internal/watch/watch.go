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
type ExportSpansFunc func(context.Context, agenttrace.Turn, int, bool, string) (int, error)
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

type progressivePlan struct {
	FirstObservationIndex int
	ExportSpans           bool
	Final                 bool
	ExportScores          bool
}

func planTurn(turn agenttrace.Turn, progress exportstate.TurnProgress) (progressivePlan, error) {
	if progress.ExportedObservationCount < 0 || progress.ExportedObservationCount > len(turn.Observations) {
		return progressivePlan{}, fmt.Errorf("exported observation count %d outside [0,%d]", progress.ExportedObservationCount, len(turn.Observations))
	}
	if turn.TraceID == "" || turn.InputText() == "" {
		return progressivePlan{}, nil
	}
	if !turn.Completed {
		if progress.FinalSpansExported {
			return progressivePlan{}, fmt.Errorf("incomplete turn has final span checkpoint")
		}
		if progress.ExportedObservationCount < len(turn.Observations) {
			return progressivePlan{FirstObservationIndex: progress.ExportedObservationCount, ExportSpans: true}, nil
		}
		return progressivePlan{}, nil
	}
	if turn.OutputText() == "" {
		return progressivePlan{}, nil
	}
	if progress.FinalSpansExported {
		if progress.ExportedObservationCount != len(turn.Observations) {
			return progressivePlan{}, fmt.Errorf("final span checkpoint has observation count %d, parsed %d", progress.ExportedObservationCount, len(turn.Observations))
		}
		return progressivePlan{ExportScores: true}, nil
	}
	return progressivePlan{
		FirstObservationIndex: progress.ExportedObservationCount,
		ExportSpans:           true,
		Final:                 true,
	}, nil
}

func InitializeState(statePath string, now time.Time, stdout io.Writer, quiet bool) (exportstate.State, error) {
	if now.IsZero() {
		now = time.Now()
	}
	state := exportstate.State{
		Version:         2,
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
	progress := state.ProgressFor(turn.TraceID)
	plan, err := planTurn(turn, progress)
	if err != nil {
		fmt.Fprintf(writerOrDiscard(opts.Stderr), "ERROR: failed to plan trace=%s path=%s: %v\n", turn.TraceID, sourcePath, err)
		return state, 0, true, nil
	}
	if !plan.ExportSpans && !plan.ExportScores {
		return state, 0, false, nil
	}
	environment := progress.Environment
	if plan.ExportSpans {
		if opts.ResolveWorkspace == nil {
			fmt.Fprintf(writerOrDiscard(opts.Stderr), "ERROR: failed to resolve workspace trace=%s path=%s: missing workspace resolver callback\n", turn.TraceID, sourcePath)
			return state, 0, true, nil
		}
		resolvedTurn, resolvedEnvironment, err := opts.ResolveWorkspace(ctx, turn)
		if err != nil {
			return state, 0, false, err
		}
		turn = resolvedTurn
		if environment == "" {
			environment = resolvedEnvironment
			state, err = mutateState(opts.StatePath, state, func(current *exportstate.State) {
				currentProgress := current.ProgressFor(turn.TraceID)
				currentProgress.Environment = environment
				current.SetProgress(turn.TraceID, currentProgress)
			})
			if err != nil {
				return state, 0, false, err
			}
		}
	}
	if environment == "" {
		return state, 0, false, fmt.Errorf("turn progress %s requires environment", turn.TraceID)
	}
	if *attemptedExport {
		if err := waitBetweenExports(ctx, opts.PollIntervalSeconds); err != nil {
			return state, 0, false, err
		}
	}
	*attemptedExport = true

	stdout := writerOrDiscard(opts.Stdout)
	stderr := writerOrDiscard(opts.Stderr)
	emitted := 0
	if plan.ExportSpans {
		if opts.ExportSpans == nil {
			fmt.Fprintf(stderr, "ERROR: failed to export trace=%s path=%s: missing span export callback\n", turn.TraceID, sourcePath)
			return state, 0, true, nil
		}
		status, err := opts.ExportSpans(ctx, turn, plan.FirstObservationIndex, plan.Final, environment)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: failed to export trace=%s observations=%d:%d final=%t path=%s: %v\n", turn.TraceID, plan.FirstObservationIndex, len(turn.Observations), plan.Final, sourcePath, err)
			return state, 0, true, nil
		}
		state, err = mutateState(opts.StatePath, state, func(current *exportstate.State) {
			progress := current.ProgressFor(turn.TraceID)
			progress.ExportedObservationCount = len(turn.Observations)
			if plan.Final {
				progress.FinalSpansExported = true
			}
			current.SetProgress(turn.TraceID, progress)
		})
		if err != nil {
			return state, 0, false, err
		}
		emitted = 1
		if !opts.Quiet {
			fmt.Fprintf(stdout, "exported trace=%s observations=%d:%d final=%t status=%d path=%s\n", turn.TraceID, plan.FirstObservationIndex, len(turn.Observations), plan.Final, status, sourcePath)
		}
		if !plan.Final {
			return state, emitted, false, nil
		}
	}

	if opts.ExportScores == nil {
		fmt.Fprintf(stderr, "ERROR: failed to score trace=%s path=%s: missing score export callback\n", turn.TraceID, sourcePath)
		return state, emitted, true, nil
	}
	if err := opts.ExportScores(ctx, turn, environment); err != nil {
		fmt.Fprintf(stderr, "ERROR: failed to score trace=%s path=%s: %v\n", turn.TraceID, sourcePath, err)
		return state, emitted, true, nil
	}
	state, err = mutateState(opts.StatePath, state, func(current *exportstate.State) {
		current.AddProcessed(turn.TraceID)
	})
	if err != nil {
		return state, emitted, false, err
	}
	if !opts.Quiet {
		fmt.Fprintf(stdout, "scored trace=%s path=%s\n", turn.TraceID, sourcePath)
	}
	return state, emitted, false, nil
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
