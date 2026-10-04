package watch

import (
	"container/list"
	"context"
	"errors"
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

type ScanOptions struct {
	Root                string
	StatePath           string
	Now                 time.Time
	Stdout              io.Writer
	Stderr              io.Writer
	Quiet               bool
	ResolveWorkspace    ResolveWorkspaceFunc
	ExportSpans         ExportSpansFunc
	PollIntervalSeconds float64
	InitialLookbackSecs int
	CaptureRollout      func(context.Context, string, string) error
}

const (
	scanCacheCapacity  = 1024
	parseRetryInterval = 30 * time.Second
)

type scanDependencies struct {
	discover func(string) ([]string, error)
	stat     func(string) (os.FileInfo, error)
	parse    func(string, func(string) bool) ([]agenttrace.Turn, error)
}

func defaultScanDependencies() scanDependencies {
	return scanDependencies{
		discover: codextrace.SessionPaths,
		stat:     os.Stat,
		parse:    codextrace.ParseTurnsFiltered,
	}
}

type scanCacheEntry struct {
	path        string
	info        os.FileInfo
	parseFailed bool
	lastAttempt time.Time
}

type scanRuntime struct {
	entries map[string]*list.Element
	lru     *list.List
}

func newScanRuntime() *scanRuntime {
	return &scanRuntime{entries: make(map[string]*list.Element), lru: list.New()}
}

func (r *scanRuntime) get(path string) *scanCacheEntry {
	if r == nil {
		return nil
	}
	element := r.entries[path]
	if element == nil {
		return nil
	}
	r.lru.MoveToFront(element)
	return element.Value.(*scanCacheEntry)
}

func (r *scanRuntime) put(entry scanCacheEntry) {
	if r == nil {
		return
	}
	if r.entries == nil {
		r.entries = make(map[string]*list.Element)
		r.lru = list.New()
	}
	if existing := r.entries[entry.path]; existing != nil {
		existing.Value = &entry
		r.lru.MoveToFront(existing)
		return
	}
	r.entries[entry.path] = r.lru.PushFront(&entry)
	if r.lru.Len() > scanCacheCapacity {
		oldest := r.lru.Back()
		delete(r.entries, oldest.Value.(*scanCacheEntry).path)
		r.lru.Remove(oldest)
	}
}

func (r *scanRuntime) remove(path string) {
	if r == nil {
		return
	}
	if element := r.entries[path]; element != nil {
		delete(r.entries, path)
		r.lru.Remove(element)
	}
}

func InitializeState(ctx context.Context, statePath string, now time.Time, stdout io.Writer, quiet bool) (exportstate.State, bool, error) {
	if now.IsZero() {
		now = time.Now()
	}
	state := exportstate.State{
		Version:         exportstate.Version,
		ScanWatermarkNS: now.Add(-time.Duration(buildinfo.DefaultInitialLookbackSecs) * time.Second).UnixNano(),
	}
	state, created, err := exportstate.LoadOrCreate(ctx, statePath, state)
	if err != nil {
		return exportstate.State{}, false, err
	}
	if created && !quiet {
		fmt.Fprintln(writerOrDiscard(stdout), "initialized watch state; historical turns before the initial watermark will not be exported")
	}
	return state, created, nil
}

func ScanOnce(ctx context.Context, opts ScanOptions, state exportstate.State) (exportstate.State, int, error) {
	return scanOnce(ctx, opts, state, newScanRuntime(), defaultScanDependencies())
}

func scanOnce(ctx context.Context, opts ScanOptions, state exportstate.State, runtime *scanRuntime, deps scanDependencies) (exportstate.State, int, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	stderr := writerOrDiscard(opts.Stderr)
	scanStartedNS := opts.Now.UnixNano()
	watermark := state.ScanWatermarkNS
	exportedCount := 0
	scanFailed := false
	attemptedExport := false
	discoveryErrors := 0
	statErrors := 0
	parseErrors := 0
	deliveryErrors := 0
	changedSources := 0

	var queueExported int
	var err error
	state, queueExported, err = drainQueue(ctx, opts, state, &attemptedExport)
	if err != nil {
		return state, queueExported, err
	}
	exportedCount += queueExported
	if err := ctx.Err(); err != nil {
		return state, exportedCount, err
	}
	processedTraceIDs := make(map[string]struct{}, len(state.ProcessedTraceIDs))
	for _, traceID := range state.ProcessedTraceIDs {
		processedTraceIDs[traceID] = struct{}{}
	}

	sessionPaths, discoveryErr := deps.discover(opts.Root)
	if discoveryErr != nil {
		scanFailed = true
		discoveryErrors += discoveryErrorCount(discoveryErr)
		if !opts.Quiet {
			fmt.Fprintf(stderr, "warning: incomplete Codex rollout discovery: %v\n", discoveryErr)
		}
	}
	for _, sessionPath := range sessionPaths {
		if err := ctx.Err(); err != nil {
			return state, exportedCount, err
		}
		info, err := deps.stat(sessionPath)
		if err != nil {
			scanFailed = true
			statErrors++
			if !opts.Quiet {
				fmt.Fprintf(stderr, "warning: skipped unreadable rollout %s: %v\n", sessionPath, err)
			}
			continue
		}
		mtimeNS := info.ModTime().UnixNano()
		if mtimeNS <= watermark || mtimeNS > scanStartedNS {
			continue
		}
		cached := runtime.get(sessionPath)
		if cached != nil && !sameSource(cached.info, info) {
			runtime.remove(sessionPath)
			cached = nil
		}
		if cached != nil && !cached.parseFailed {
			continue
		}
		if cached != nil && opts.Now.Sub(cached.lastAttempt) < parseRetryInterval {
			scanFailed = true
			parseErrors++
			if !opts.Quiet {
				fmt.Fprintf(stderr, "warning: deferred retry for unchanged unreadable rollout %s\n", sessionPath)
			}
			continue
		}

		turns, err := deps.parse(sessionPath, func(traceID string) bool {
			_, processed := processedTraceIDs[traceID]
			return !processed
		})
		if err != nil {
			scanFailed = true
			parseErrors++
			runtime.put(scanCacheEntry{path: sessionPath, info: info, parseFailed: true, lastAttempt: opts.Now})
			if !opts.Quiet {
				fmt.Fprintf(stderr, "warning: skipped unreadable rollout %s: %v\n", sessionPath, err)
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return state, exportedCount, err
		}
		sourceDeliveryFailed := false
		if opts.CaptureRollout != nil {
			sessionID := ""
			if len(turns) > 0 {
				sessionID = turns[0].SessionID
			}
			if err := opts.CaptureRollout(ctx, sessionPath, sessionID); err != nil {
				fmt.Fprintf(stderr, "ERROR: official_plugin_capture_failed path=%s: %v\n", sessionPath, err)
				scanFailed = true
				sourceDeliveryFailed = true
				deliveryErrors++
			}
		}
		for _, turn := range turns {
			if err := ctx.Err(); err != nil {
				return state, exportedCount, err
			}
			if _, processed := processedTraceIDs[turn.TraceID]; processed {
				continue
			}
			var emitted int
			var failed bool
			state, emitted, failed, err = processTurn(ctx, opts, state, turn, sessionPath, &attemptedExport)
			if err != nil {
				return state, exportedCount + emitted, err
			}
			exportedCount += emitted
			if failed {
				scanFailed = true
				deliveryErrors++
				sourceDeliveryFailed = true
			}
			if state.HasProcessed(turn.TraceID) {
				processedTraceIDs[turn.TraceID] = struct{}{}
			}
		}
		after, err := deps.stat(sessionPath)
		if err != nil {
			scanFailed = true
			statErrors++
			runtime.remove(sessionPath)
			if !opts.Quiet {
				fmt.Fprintf(stderr, "warning: rollout changed or became unreadable during scan %s: %v\n", sessionPath, err)
			}
			continue
		}
		if !sameSource(info, after) {
			scanFailed = true
			changedSources++
			runtime.remove(sessionPath)
			if !opts.Quiet {
				fmt.Fprintf(stderr, "warning: rollout changed during scan %s\n", sessionPath)
			}
			continue
		}
		if sourceDeliveryFailed || hasPendingScore(turns, processedTraceIDs, state) {
			runtime.remove(sessionPath)
			continue
		}
		runtime.put(scanCacheEntry{path: sessionPath, info: after})
	}

	if scanFailed {
		fmt.Fprintf(stderr, "ERROR: watch_scan_incomplete discovery_errors=%d stat_errors=%d parse_errors=%d delivery_errors=%d changed_sources=%d watermark_advanced=false\n", discoveryErrors, statErrors, parseErrors, deliveryErrors, changedSources)
	}
	if err := ctx.Err(); err != nil {
		return state, exportedCount, err
	}
	if !scanFailed {
		state, err = mutateState(ctx, opts, state, func(current *exportstate.State) {
			current.ScanWatermarkNS = scanStartedNS
		})
		if err != nil {
			return state, exportedCount, err
		}
	}
	return state, exportedCount, nil
}

func sameSource(before, after os.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().UnixNano() == after.ModTime().UnixNano()
}

func discoveryErrorCount(err error) int {
	var counted interface{ ErrorCount() int }
	if errors.As(err, &counted) && counted.ErrorCount() > 0 {
		return counted.ErrorCount()
	}
	return 1
}

func hasPendingScore(turns []agenttrace.Turn, processed map[string]struct{}, state exportstate.State) bool {
	for _, turn := range turns {
		if _, done := processed[turn.TraceID]; !done && state.PendingScoreEnvironment(turn.TraceID) != "" {
			return true
		}
	}
	return false
}

func processTurn(ctx context.Context, opts ScanOptions, state exportstate.State, turn agenttrace.Turn, sourcePath string, attemptedExport *bool) (exportstate.State, int, bool, error) {
	traceID := turn.TraceID
	environment := state.PendingScoreEnvironment(traceID)
	progress := state.TurnProgress[traceID]
	if turn.Completed && !isExportable(turn) {
		return state, 0, false, nil
	}
	if !isExportable(turn) && (turn.TraceID == "" || turn.InputText() == "" || (len(turn.Observations) == progress.ObservationCount && len(turn.ModelCalls) == progress.ModelCallCount)) {
		return state, 0, false, nil
	}
	if progress.ObservationCount < 0 || progress.ModelCallCount < 0 || progress.ObservationCount > len(turn.Observations) || progress.ModelCallCount > len(turn.ModelCalls) {
		return state, 0, false, fmt.Errorf("observation prefix shrank for trace %s", traceID)
	}
	if progress.Environment != "" {
		environment = progress.Environment
	}

	if opts.ResolveWorkspace == nil {
		fmt.Fprintf(writerOrDiscard(opts.Stderr), "ERROR: failed to resolve workspace trace=%s path=%s: missing workspace resolver callback\n", traceID, sourcePath)
		return state, 0, true, nil
	}
	resolvedTurn, resolvedEnvironment, err := opts.ResolveWorkspace(ctx, turn)
	if err != nil {
		return state, 0, false, err
	}
	turn = resolvedTurn
	if environment == "" {
		environment = resolvedEnvironment
	}
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
	turn.ExportDelta = true
	turn.FirstObservation = progress.ObservationCount
	turn.FirstModelCall = progress.ModelCallCount
	status, err := opts.ExportSpans(ctx, turn, environment)
	if err != nil {
		fmt.Fprintf(writerOrDiscard(opts.Stderr), "ERROR: failed to export trace=%s path=%s: %v\n", traceID, sourcePath, err)
		return state, 0, true, nil
	}
	if !opts.Quiet {
		fmt.Fprintf(writerOrDiscard(opts.Stdout), "span_export_succeeded trace=%s status=%d checkpoint=pending\n", traceID, status)
	}
	state, err = mutateState(ctx, opts, state, func(current *exportstate.State) {
		if turn.Completed {
			current.AddProcessed(traceID)
		} else {
			if current.TurnProgress == nil {
				current.TurnProgress = map[string]exportstate.TurnProgress{}
			}
			current.TurnProgress[traceID] = exportstate.TurnProgress{ObservationCount: len(turn.Observations), ModelCallCount: len(turn.ModelCalls), Environment: environment}
		}
	})
	if err != nil {
		fmt.Fprintf(writerOrDiscard(opts.Stderr), "ERROR: span_checkpoint_unconfirmed trace=%s export_result=success replay_possible=true\n", traceID)
		return state, 1, false, err
	}
	if !opts.Quiet {
		if turn.Completed {
			fmt.Fprintf(writerOrDiscard(opts.Stdout), "processed trace=%s path=%s\n", traceID, sourcePath)
		} else {
			fmt.Fprintf(writerOrDiscard(opts.Stdout), "progress trace=%s observations=%d model_calls=%d\n", traceID, len(turn.Observations), len(turn.ModelCalls))
		}
	}
	return state, boolToInt(turn.Completed), false, nil
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
		for _, turn := range turns {
			if turn.TraceID == "" || turn.InputText() == "" {
				continue
			}
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
			if failed || (turn.Completed && !state.HasProcessed(turn.TraceID)) || (!turn.Completed && len(turn.Observations) == 0 && len(turn.ModelCalls) == 0) {
				requestComplete = false
			}
		}
		if hasExportableTurn && requestComplete {
			state, err = mutateState(ctx, opts, state, func(current *exportstate.State) {
				current.RemoveQueued(request)
			})
			if err != nil {
				return state, exportedCount, err
			}
		}
	}
	return state, exportedCount, nil
}

func mutateState(ctx context.Context, opts ScanOptions, state exportstate.State, mutate func(*exportstate.State)) (exportstate.State, error) {
	if opts.StatePath == "" {
		mutate(&state)
		return state, nil
	}
	return retryStateOperation(ctx, opts, state, func() (exportstate.State, error) {
		return exportstate.Update(ctx, opts.StatePath, func(current *exportstate.State) error {
			mutate(current)
			return nil
		})
	})
}

const (
	initialStateLockRetryDelay = time.Second
	maximumStateLockRetryDelay = 30 * time.Second
	stateLockLogInterval       = time.Minute
)

type stateLockRetryPolicy struct {
	initialDelay time.Duration
	maximumDelay time.Duration
	logInterval  time.Duration
	now          func() time.Time
	wait         func(context.Context, time.Duration) error
}

func retryStateOperation(ctx context.Context, opts ScanOptions, previous exportstate.State, operation func() (exportstate.State, error)) (exportstate.State, error) {
	return retryStateOperationWithPolicy(ctx, opts, previous, operation, stateLockRetryPolicy{
		initialDelay: initialStateLockRetryDelay,
		maximumDelay: maximumStateLockRetryDelay,
		logInterval:  stateLockLogInterval,
		now:          time.Now,
		wait:         waitStateLockRetry,
	})
}

func retryStateOperationWithPolicy(ctx context.Context, opts ScanOptions, previous exportstate.State, operation func() (exportstate.State, error), policy stateLockRetryPolicy) (exportstate.State, error) {
	delay := policy.initialDelay
	var started time.Time
	var lastLogged time.Time
	for {
		state, err := operation()
		if err == nil {
			if !started.IsZero() && !opts.Quiet {
				fmt.Fprintf(writerOrDiscard(opts.Stdout), "export state lock recovered path=%s waited=%s\n", opts.StatePath, policy.now().Sub(started).Round(time.Millisecond))
			}
			return state, nil
		}
		if !errors.Is(err, exportstate.ErrLockBusy) {
			return previous, err
		}
		now := policy.now()
		if started.IsZero() {
			started = now
		}
		if lastLogged.IsZero() || now.Sub(lastLogged) >= policy.logInterval {
			var busyErr *exportstate.LockBusyError
			attemptWait := time.Duration(0)
			if errors.As(err, &busyErr) {
				attemptWait = busyErr.Waited
			}
			fmt.Fprintf(writerOrDiscard(opts.Stderr), "ERROR: export state lock busy path=%s elapsed=%s retry_in=%s: %v\n", opts.StatePath, (now.Sub(started) + attemptWait).Round(time.Millisecond), delay, err)
			lastLogged = now
		}
		if err := policy.wait(ctx, delay); err != nil {
			return previous, err
		}
		delay = nextStateLockRetryDelay(delay, policy.maximumDelay)
	}
}

func waitStateLockRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextStateLockRetryDelay(current, maximum time.Duration) time.Duration {
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
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
	current, err := retryStateOperation(ctx, opts, exportstate.State{}, func() (exportstate.State, error) {
		state, _, err := InitializeState(ctx, opts.StatePath, time.Now(), opts.Stdout, opts.Quiet)
		return state, err
	})
	if err != nil {
		return err
	}
	if !opts.Quiet {
		fmt.Fprintf(writerOrDiscard(opts.Stdout), "watching %s\n", opts.Root)
	}
	runtime := newScanRuntime()
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
		current, _, err = scanOnce(ctx, opts, current, runtime, defaultScanDependencies())
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
