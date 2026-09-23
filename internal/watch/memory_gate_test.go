package watch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
)

const (
	memoryGateLimitMiB = 768
)

type preparedMemoryCase struct {
	root     string
	state    string
	specPath string
	spec     memoryGateSpec
}

// TestWatchMemoryEnvelope runs opt-in resource cases in fresh, memory-limited
// processes. Fixture creation and state construction stay in the parent.
func TestWatchMemoryEnvelope(t *testing.T) {
	if os.Getenv(memoryGateWorkerEnv) == "1" {
		runMemoryGateWorker(t)
		return
	}
	if os.Getenv("CODEX_LANGFUSE_RUN_MEMORY_GATE") != "1" {
		t.Skip("set CODEX_LANGFUSE_RUN_MEMORY_GATE=1 to run the isolated watcher memory matrix")
	}
	if os.Geteuid() == 0 {
		t.Skip("the memory gate requires the user's systemd manager to cap each worker cgroup")
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		t.Fatalf("systemd-run is required to isolate memory-gate workers: %v", err)
	}
	if _, err := exec.LookPath("/usr/bin/time"); err != nil {
		t.Fatalf("/usr/bin/time is required to measure worker RSS: %v", err)
	}
	baselineBinary := os.Getenv(memoryGateBaselineBinaryEnv)
	if baselineBinary == "" {
		t.Fatalf("%s must point to a test binary built from the installed baseline with TestLegacyWatchMemoryEnvelope", memoryGateBaselineBinaryEnv)
	}
	if !filepath.IsAbs(baselineBinary) {
		t.Fatalf("%s must be an absolute path", memoryGateBaselineBinaryEnv)
	}
	baselineInfo, err := os.Stat(baselineBinary)
	if err != nil || !baselineInfo.Mode().IsRegular() || baselineInfo.Mode().Perm()&0o111 == 0 {
		t.Fatalf("baseline test binary is missing or not executable: %s (%v)", baselineBinary, err)
	}

	scratch := largeProbeScratchParent(t)
	root, err := os.MkdirTemp(scratch, "codex-langfuse-memory-gate-")
	if err != nil {
		t.Fatalf("create disk-backed memory-gate scratch: %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		t.Fatalf("protect memory-gate scratch: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	cases := []struct {
		name string
		make func(string) (preparedMemoryCase, error)
	}{
		{"P100", prepareProcessedHistoryCase},
		{"P400", prepareProcessedHistoryCase},
		{"U100", prepareUnprocessedBacklogCase},
		{"T100", prepareLargeSelectedTurnCase},
		{"R16", prepareLargeRecordCase},
		{"S100", preparePendingScoreCase},
		{"C100", prepareCorruptSiblingCase},
	}
	var binaryPath string
	results := []string{fmt.Sprintf("candidate_toolchain go=%s goos=%s goarch=%s gomaxprocs=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0))}
	for _, item := range cases {
		caseDir := filepath.Join(root, item.name)
		if err := os.Mkdir(caseDir, 0o700); err != nil {
			t.Fatalf("create %s case directory: %v", item.name, err)
		}
		prepared, err := item.make(caseDir)
		if err != nil {
			t.Fatalf("prepare %s case: %v", item.name, err)
		}
		info, err := os.Stat(filepath.Join(prepared.root, "sessions", "memory", "rollout-primary.jsonl"))
		if err != nil {
			t.Fatalf("stat %s primary source: %v", item.name, err)
		}
		if info.Size() < prepared.spec.TargetBytes {
			t.Fatalf("%s source is %d bytes, below target %d", item.name, info.Size(), prepared.spec.TargetBytes)
		}
		maxRecordBytes, err := memoryGateMaxRecordBytes(filepath.Join(prepared.root, "sessions", "memory", "rollout-primary.jsonl"))
		if err != nil {
			t.Fatalf("measure %s maximum JSONL record: %v", item.name, err)
		}
		if item.name == "R16" && maxRecordBytes < int64(prepared.spec.MinOutputBytes) {
			t.Fatalf("R16 maximum record is %d bytes, below the %d-byte payload", maxRecordBytes, prepared.spec.MinOutputBytes)
		}
		prepared.spec.MaxRecordBytes = maxRecordBytes
		if err := writeMemorySpec(prepared.specPath, prepared.spec); err != nil {
			t.Fatalf("update %s measured record size: %v", item.name, err)
		}
		if binaryPath == "" {
			binaryPath, err = os.Executable()
			if err != nil {
				t.Fatalf("find current test binary: %v", err)
			}
		}
		samples := make([]int64, 0, 5)
		for sample := 0; sample < 5; sample++ {
			usagePath := filepath.Join(caseDir, fmt.Sprintf("sample-%d-time.txt", sample))
			command := exec.Command("systemd-run", "--user", "--scope",
				"--property=MemoryMax=768M", "--property=MemorySwapMax=0", "--",
				"/usr/bin/time", "-f", "%M %e", "-o", usagePath,
				binaryPath, "-test.run=^TestWatchMemoryEnvelope$", "-test.count=1", "-test.timeout=30m", "-test.v")
			command.Env = append(os.Environ(),
				memoryGateWorkerEnv+"=1",
				memoryGateSpecEnv+"="+prepared.specPath,
				memoryGateRootEnv+"="+prepared.root,
				memoryGateStateEnv+"="+prepared.state,
				memoryGateTimeEnv+"=2026-09-22T12:01:00Z",
			)
			output, runErr := command.CombinedOutput()
			if runErr != nil {
				t.Fatalf("%s sample %d worker failed (%v): %s", item.name, sample+1, runErr, strings.TrimSpace(string(output)))
			}
			results = append(results, strings.TrimSpace(string(output)))
			usageRaw, err := os.ReadFile(usagePath)
			if err != nil {
				t.Fatalf("read %s sample %d RSS: %v", item.name, sample+1, err)
			}
			usageFields := strings.Fields(string(usageRaw))
			if len(usageFields) != 2 {
				t.Fatalf("parse %s sample %d resource measurement %q", item.name, sample+1, usageRaw)
			}
			rssKiB, err := strconv.ParseInt(usageFields[0], 10, 64)
			if err != nil {
				t.Fatalf("parse %s sample %d RSS %q: %v", item.name, sample+1, usageFields[0], err)
			}
			samples = append(samples, rssKiB)
			results = append(results, fmt.Sprintf("%s sample=%d elapsed_seconds=%s rss_kib=%d", item.name, sample+1, usageFields[1], rssKiB))
		}
		worstKiB := int64(0)
		for _, value := range samples {
			if value > worstKiB {
				worstKiB = value
			}
		}
		results = append(results, fmt.Sprintf("%s source_bytes=%d source_target_bytes=%d max_record_bytes=%d rss_kib=%v worst_mib=%.1f", item.name, info.Size(), prepared.spec.TargetBytes, maxRecordBytes, samples, float64(worstKiB)/1024))
		if worstKiB > memoryGateLimitMiB*1024 {
			t.Errorf("%s worker peak %d KiB exceeded fixed %d MiB allowance", item.name, worstKiB, memoryGateLimitMiB)
		}

		baselineSamples := make([]int64, 0, 5)
		for sample := 0; sample < 5; sample++ {
			usagePath := filepath.Join(caseDir, fmt.Sprintf("baseline-sample-%d-time.txt", sample))
			command := exec.Command("systemd-run", "--user", "--scope",
				"--property=MemoryMax=768M", "--property=MemorySwapMax=0", "--",
				"/usr/bin/time", "-f", "%M %e", "-o", usagePath,
				baselineBinary, "-test.run=^TestLegacyWatchMemoryEnvelope$", "-test.count=1", "-test.timeout=30m", "-test.v")
			command.Env = append(os.Environ(),
				memoryGateLegacyWorkerEnv+"=1",
				memoryGateSpecEnv+"="+prepared.specPath,
				memoryGateRootEnv+"="+prepared.root,
				memoryGateStateEnv+"="+prepared.state,
				memoryGateTimeEnv+"=2026-09-22T12:01:00Z",
			)
			output, runErr := command.CombinedOutput()
			if runErr != nil {
				t.Fatalf("installed-baseline %s sample %d worker failed (%v): %s", item.name, sample+1, runErr, strings.TrimSpace(string(output)))
			}
			results = append(results, strings.TrimSpace(string(output)))
			usageRaw, err := os.ReadFile(usagePath)
			if err != nil {
				t.Fatalf("read baseline %s sample %d RSS: %v", item.name, sample+1, err)
			}
			usageFields := strings.Fields(string(usageRaw))
			if len(usageFields) != 2 {
				t.Fatalf("parse baseline %s sample %d resource measurement %q", item.name, sample+1, usageRaw)
			}
			rssKiB, err := strconv.ParseInt(usageFields[0], 10, 64)
			if err != nil {
				t.Fatalf("parse baseline %s sample %d RSS %q: %v", item.name, sample+1, usageFields[0], err)
			}
			baselineSamples = append(baselineSamples, rssKiB)
			results = append(results, fmt.Sprintf("baseline %s sample=%d elapsed_seconds=%s rss_kib=%d", item.name, sample+1, usageFields[1], rssKiB))
		}
		baselineWorstKiB := int64(0)
		for _, value := range baselineSamples {
			if value > baselineWorstKiB {
				baselineWorstKiB = value
			}
		}
		results = append(results, fmt.Sprintf("baseline %s source_bytes=%d source_target_bytes=%d max_record_bytes=%d rss_kib=%v worst_mib=%.1f", item.name, info.Size(), prepared.spec.TargetBytes, maxRecordBytes, baselineSamples, float64(baselineWorstKiB)/1024))
	}
	for _, result := range results {
		t.Log(result)
	}
}

func TestMemoryGateProcessedHistoryFixtureSelectsEOFTurn(t *testing.T) {
	root := t.TempDir()
	prepared, err := prepareProcessedHistoryCaseWithTarget(root, "P100", 1<<10)
	if err != nil {
		t.Fatalf("prepare small P100 fixture: %v", err)
	}
	if prepared.spec.ExpectedTraceID != memoryGateEOFTurnTraceID || prepared.spec.ExpectedSelectedTurns != 1 || prepared.spec.ExpectedSpanCalls != 1 || prepared.spec.ExpectedScoreCalls != 1 {
		t.Fatalf("P100 expected oracle = %+v", prepared.spec)
	}
	state, err := exportstate.Load(prepared.state)
	if err != nil || state == nil {
		t.Fatalf("load generated P100 state: state=%t err=%v", state != nil, err)
	}
	var sourceTurns, selectedTurns, parseCalls, spanCalls, scoreCalls int
	deps := defaultScanDependencies()
	originalParse := deps.parse
	deps.parse = func(path string, include func(string) bool) ([]agenttrace.Turn, error) {
		parseCalls++
		return originalParse(path, func(traceID string) bool {
			sourceTurns++
			selected := include(traceID)
			if selected {
				selectedTurns++
			}
			return selected
		})
	}
	now := memoryGateNow()
	scanned, exported, err := scanOnce(context.Background(), ScanOptions{
		Root:  prepared.root,
		Now:   now,
		Quiet: true,
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			return turn, "memory-test", nil
		},
		ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ string) (int, error) {
			spanCalls++
			if turn.TraceID != memoryGateEOFTurnTraceID || !turn.Completed || turn.InputText() != "new input" || turn.OutputText() != "new output" {
				t.Errorf("EOF turn projection = trace:%q completed:%t input:%q output:%q", turn.TraceID, turn.Completed, turn.InputText(), turn.OutputText())
			}
			return 200, nil
		},
		ExportScores: func(_ context.Context, turn agenttrace.Turn, _ string) error {
			scoreCalls++
			if turn.TraceID != memoryGateEOFTurnTraceID || !turn.Completed {
				t.Errorf("EOF score turn = trace:%q completed:%t", turn.TraceID, turn.Completed)
			}
			return nil
		},
	}, *state, newScanRuntime(), deps)
	if err != nil {
		t.Fatalf("scan small P100 fixture: %v", err)
	}
	if parseCalls != 1 || sourceTurns != prepared.spec.ExpectedSourceTurns || selectedTurns != 1 || spanCalls != 1 || scoreCalls != 1 || exported != 1 || !scanned.HasProcessed(memoryGateEOFTurnTraceID) || scanned.ScanWatermarkNS != now.UnixNano() {
		t.Fatalf("P100 fixture oracle: parses=%d source=%d selected=%d spans=%d scores=%d exported=%d state=%+v", parseCalls, sourceTurns, selectedTurns, spanCalls, scoreCalls, exported, scanned)
	}
}

func memoryGateMaxRecordBytes(path string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 128<<10)
	var maximum int64
	for {
		line, readErr := reader.ReadBytes('\n')
		recordBytes := len(line)
		if recordBytes > 0 && line[recordBytes-1] == '\n' {
			recordBytes--
		}
		if int64(recordBytes) > maximum {
			maximum = int64(recordBytes)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return maximum, nil
			}
			return 0, readErr
		}
	}
}

func runMemoryGateWorker(t *testing.T) {
	t.Helper()
	var spec memoryGateSpec
	specRaw, err := os.ReadFile(os.Getenv(memoryGateSpecEnv))
	if err != nil {
		t.Fatalf("read memory worker spec: %v", err)
	}
	if err := json.Unmarshal(specRaw, &spec); err != nil {
		t.Fatalf("decode memory worker spec: %v", err)
	}
	state, err := exportstate.Load(os.Getenv(memoryGateStateEnv))
	if err != nil || state == nil {
		t.Fatalf("load private memory worker state: state=%t err=%v", state != nil, err)
	}
	now, err := time.Parse(time.RFC3339Nano, os.Getenv(memoryGateTimeEnv))
	if err != nil {
		t.Fatalf("parse memory worker time: %v", err)
	}
	root := os.Getenv(memoryGateRootEnv)
	deps := defaultScanDependencies()
	parsed := make(map[string]int)
	sourceTurnContexts := 0
	selectedTurnContexts := 0
	originalParse := deps.parse
	deps.parse = func(path string, include func(string) bool) ([]agenttrace.Turn, error) {
		parsed[filepath.Base(path)]++
		return originalParse(path, func(traceID string) bool {
			sourceTurnContexts++
			selected := include(traceID)
			if selected {
				selectedTurnContexts++
			}
			return selected
		})
	}
	spanCalls := 0
	scoreCalls := 0
	runtime := newScanRuntime()
	scannedState, exported, err := scanOnce(context.Background(), ScanOptions{
		Root:  root,
		Now:   now,
		Quiet: true,
		ResolveWorkspace: func(_ context.Context, turn agenttrace.Turn) (agenttrace.Turn, string, error) {
			return turn, "memory-test", nil
		},
		ExportSpans: func(_ context.Context, turn agenttrace.Turn, _ string) (int, error) {
			spanCalls++
			if !turn.Completed || turn.InputText() == "" || turn.OutputText() == "" {
				t.Errorf("case %s exported an incomplete projection: completed=%t input_bytes=%d output_bytes=%d", spec.Name, turn.Completed, len(turn.InputText()), len(turn.OutputText()))
			}
			if spec.ExpectedTraceID != "" && turn.TraceID != spec.ExpectedTraceID {
				t.Errorf("case %s span trace=%q, want %q", spec.Name, turn.TraceID, spec.ExpectedTraceID)
			}
			if spec.Name == "U100" && len(turn.InputText()) < spec.MinInputBytes {
				t.Errorf("U100 selected input was truncated: bytes=%d", len(turn.InputText()))
			}
			if spec.Name == "T100" && (len(turn.Observations) != spec.ExpectedObsPerTurn || len(turn.InputText()) < spec.MinInputBytes) {
				t.Errorf("T100 projection changed: input_bytes=%d observations=%d", len(turn.InputText()), len(turn.Observations))
			}
			if spec.Name == "R16" && (len(turn.Observations) == 0 || len(turn.Observations[0].Output) < spec.MinOutputBytes) {
				t.Errorf("R16 large record was truncated: observations=%d", len(turn.Observations))
			}
			return 200, nil
		},
		ExportScores: func(_ context.Context, turn agenttrace.Turn, environment string) error {
			scoreCalls++
			if spec.ExpectedTraceID != "" && turn.TraceID != spec.ExpectedTraceID {
				t.Errorf("case %s score trace=%q, want %q", spec.Name, turn.TraceID, spec.ExpectedTraceID)
			}
			if spec.Name == "S100" && environment != "persisted-score-environment" {
				t.Errorf("S100 environment=%q", environment)
			}
			return nil
		},
	}, *state, runtime, deps)
	if err != nil {
		t.Fatalf("scan memory case %s: %v", spec.Name, err)
	}
	if parsed["rollout-primary.jsonl"] != 1 {
		t.Fatalf("case %s parsed primary %d times, want 1", spec.Name, parsed["rollout-primary.jsonl"])
	}
	if sourceTurnContexts != spec.ExpectedSourceTurns || selectedTurnContexts != spec.ExpectedSelectedTurns {
		t.Fatalf("case %s parsed turn counts: source=%d selected=%d; want %d/%d", spec.Name, sourceTurnContexts, selectedTurnContexts, spec.ExpectedSourceTurns, spec.ExpectedSelectedTurns)
	}
	if spanCalls != spec.ExpectedSpanCalls || scoreCalls != spec.ExpectedScoreCalls || exported != spec.ExpectedSpanCalls {
		t.Fatalf("case %s callbacks: spans=%d scores=%d exported=%d; want %d/%d/%d", spec.Name, spanCalls, scoreCalls, exported, spec.ExpectedSpanCalls, spec.ExpectedScoreCalls, spec.ExpectedSpanCalls)
	}
	wantWatermark := now.UnixNano()
	if spec.CorruptSibling {
		wantWatermark = now.Add(-time.Minute).UnixNano()
	}
	if scannedState.ScanWatermarkNS != wantWatermark {
		t.Fatalf("case %s watermark=%d, want %d", spec.Name, scannedState.ScanWatermarkNS, wantWatermark)
	}
	if spec.CorruptSibling {
		if parsed["rollout-z-corrupt.jsonl"] != 1 {
			t.Fatalf("C100 corrupt source parse calls=%d, want 1", parsed["rollout-z-corrupt.jsonl"])
		}
		_, _, err = scanOnce(context.Background(), ScanOptions{Root: root, Now: now.Add(time.Second), Quiet: true}, scannedState, runtime, deps)
		if err != nil {
			t.Fatalf("C100 second scan: %v", err)
		}
		if parsed["rollout-primary.jsonl"] != 1 || parsed["rollout-z-corrupt.jsonl"] != 1 {
			t.Fatalf("C100 repeated stable parses: %v", parsed)
		}
	}
	t.Logf("memory_case name=%s scans=%d source_turn_contexts=%d selected_turn_contexts=%d processed_turn_contexts=%d primary_parse_calls=%d spans=%d scores=%d processed_state=%d pending_scores=%d", spec.Name, 1+boolToInt(spec.CorruptSibling), sourceTurnContexts, selectedTurnContexts, sourceTurnContexts-selectedTurnContexts, parsed["rollout-primary.jsonl"], spanCalls, scoreCalls, len(scannedState.ProcessedTraceIDs), len(scannedState.PendingScores))
}

func prepareProcessedHistoryCase(root string) (preparedMemoryCase, error) {
	name := filepath.Base(root)
	target := int64(100 << 20)
	if name == "P400" {
		target = 400 << 20
	}
	return prepareProcessedHistoryCaseWithTarget(root, name, target)
}

func prepareProcessedHistoryCaseWithTarget(root, name string, target int64) (preparedMemoryCase, error) {
	message := strings.Repeat("p", 2048)
	turnBytes := int64(len(memoryContextLine("processed", 0)) + len(memoryCommentLine(message)))
	turns := int((target+turnBytes-1)/turnBytes) + 1
	state := newMemoryState()
	file, sourcePath, err := createMemorySource(root, "rollout-primary.jsonl")
	if err != nil {
		return preparedMemoryCase{}, err
	}
	writer := bufio.NewWriterSize(file, 1<<20)
	if err := writeMemoryHeader(writer); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	for index := 0; index < turns; index++ {
		traceID := fmt.Sprintf("trace-processed-%09d", index)
		if err := writeMemoryTurnContext(writer, "processed", index, traceID); err != nil {
			_ = file.Close()
			return preparedMemoryCase{}, err
		}
		if _, err := fmt.Fprintln(writer, memoryCommentLine(message)); err != nil {
			_ = file.Close()
			return preparedMemoryCase{}, err
		}
		state.ProcessedTraceIDs = append(state.ProcessedTraceIDs, traceID)
	}
	if err := writeMemoryTurnContext(writer, "new-at-eof", 0, memoryGateEOFTurnTraceID); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := writeMemoryInputOutput(writer, "new input", "new output"); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := writeMemoryComplete(writer); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := finishMemorySource(file, writer, sourcePath); err != nil {
		return preparedMemoryCase{}, err
	}
	spec := memoryGateSpec{Name: name, TargetBytes: target, ExpectedSpanCalls: 1, ExpectedScoreCalls: 1, ExpectedSourceTurns: turns + 1, ExpectedSelectedTurns: 1, ExpectedTraceID: memoryGateEOFTurnTraceID}
	return finishPreparedMemoryCase(root, state, spec)
}

func prepareUnprocessedBacklogCase(root string) (preparedMemoryCase, error) {
	const target = int64(100 << 20)
	const messageBytes = 100 * 1024
	message := strings.Repeat("u", messageBytes)
	turns := int(target/int64(messageBytes+256)) + 1
	state := newMemoryState()
	file, sourcePath, err := createMemorySource(root, "rollout-primary.jsonl")
	if err != nil {
		return preparedMemoryCase{}, err
	}
	writer := bufio.NewWriterSize(file, 1<<20)
	if err := writeMemoryHeader(writer); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	for index := 0; index < turns; index++ {
		traceID := fmt.Sprintf("trace-unprocessed-%09d", index)
		if err := writeMemoryTurnContext(writer, "unprocessed", index, traceID); err != nil {
			_ = file.Close()
			return preparedMemoryCase{}, err
		}
		if err := writeMemoryInputOutput(writer, message, "output"); err != nil {
			_ = file.Close()
			return preparedMemoryCase{}, err
		}
		if err := writeMemoryComplete(writer); err != nil {
			_ = file.Close()
			return preparedMemoryCase{}, err
		}
	}
	if err := finishMemorySource(file, writer, sourcePath); err != nil {
		return preparedMemoryCase{}, err
	}
	spec := memoryGateSpec{Name: "U100", TargetBytes: target, ExpectedSpanCalls: turns, ExpectedScoreCalls: turns, ExpectedSourceTurns: turns, ExpectedSelectedTurns: turns, MinInputBytes: messageBytes}
	return finishPreparedMemoryCase(root, state, spec)
}

func prepareLargeSelectedTurnCase(root string) (preparedMemoryCase, error) {
	const target = int64(100 << 20)
	const recordBytes = 1 << 20
	message := strings.Repeat("t", recordBytes)
	observations := int(target / recordBytes)
	state := newMemoryState()
	file, sourcePath, err := createMemorySource(root, "rollout-primary.jsonl")
	if err != nil {
		return preparedMemoryCase{}, err
	}
	writer := bufio.NewWriterSize(file, 1<<20)
	if err := writeMemoryHeader(writer); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	traceID := "trace-large-selected-turn"
	if err := writeMemoryTurnContext(writer, "large-selected", 0, traceID); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := writeMemoryInputOutput(writer, "input", "output"); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	for index := 0; index < observations; index++ {
		line := fmt.Sprintf(`{"timestamp":"2026-09-22T12:00:02Z","type":"response_item","payload":{"type":"reasoning","summary":[%q]}}`, message)
		if _, err := fmt.Fprintln(writer, line); err != nil {
			_ = file.Close()
			return preparedMemoryCase{}, err
		}
	}
	if err := writeMemoryComplete(writer); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := finishMemorySource(file, writer, sourcePath); err != nil {
		return preparedMemoryCase{}, err
	}
	spec := memoryGateSpec{Name: "T100", TargetBytes: target, ExpectedSpanCalls: 1, ExpectedScoreCalls: 1, ExpectedSourceTurns: 1, ExpectedSelectedTurns: 1, ExpectedTraceID: traceID, ExpectedObsPerTurn: observations, MinInputBytes: len("input")}
	return finishPreparedMemoryCase(root, state, spec)
}

func prepareLargeRecordCase(root string) (preparedMemoryCase, error) {
	const target = int64(16 << 20)
	message := strings.Repeat("r", int(target))
	state := newMemoryState()
	file, sourcePath, err := createMemorySource(root, "rollout-primary.jsonl")
	if err != nil {
		return preparedMemoryCase{}, err
	}
	writer := bufio.NewWriterSize(file, 1<<20)
	if err := writeMemoryHeader(writer); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := writeMemoryTurnContext(writer, "large-record", 0, "trace-large-record"); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := writeMemoryInputOutput(writer, "input", "output"); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	line := fmt.Sprintf(`{"timestamp":"2026-09-22T12:00:03Z","type":"response_item","payload":{"type":"reasoning","summary":[%q]}}`, message)
	if _, err := fmt.Fprintln(writer, line); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := writeMemoryComplete(writer); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := finishMemorySource(file, writer, sourcePath); err != nil {
		return preparedMemoryCase{}, err
	}
	spec := memoryGateSpec{Name: "R16", TargetBytes: target, ExpectedSpanCalls: 1, ExpectedScoreCalls: 1, ExpectedSourceTurns: 1, ExpectedSelectedTurns: 1, ExpectedTraceID: "trace-large-record", MinInputBytes: len("input"), MinOutputBytes: int(target)}
	return finishPreparedMemoryCase(root, state, spec)
}

func preparePendingScoreCase(root string) (preparedMemoryCase, error) {
	const target = int64(100 << 20)
	message := strings.Repeat("p", 2048)
	turnBytes := int64(len(memoryContextLine("processed", 0)) + len(memoryCommentLine(message)))
	turns := int((target+turnBytes-1)/turnBytes) + 1
	state := newMemoryState()
	file, sourcePath, err := createMemorySource(root, "rollout-primary.jsonl")
	if err != nil {
		return preparedMemoryCase{}, err
	}
	writer := bufio.NewWriterSize(file, 1<<20)
	if err := writeMemoryHeader(writer); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	for index := 0; index < turns; index++ {
		traceID := fmt.Sprintf("trace-processed-%09d", index)
		if err := writeMemoryTurnContext(writer, "processed", index, traceID); err != nil {
			_ = file.Close()
			return preparedMemoryCase{}, err
		}
		if _, err := fmt.Fprintln(writer, memoryCommentLine(message)); err != nil {
			_ = file.Close()
			return preparedMemoryCase{}, err
		}
		state.ProcessedTraceIDs = append(state.ProcessedTraceIDs, traceID)
	}
	const pendingTraceID = "trace-pending-score-retry"
	if err := writeMemoryTurnContext(writer, "pending-score", 0, pendingTraceID); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := writeMemoryInputOutput(writer, "pending input", "pending output"); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := writeMemoryComplete(writer); err != nil {
		_ = file.Close()
		return preparedMemoryCase{}, err
	}
	if err := finishMemorySource(file, writer, sourcePath); err != nil {
		return preparedMemoryCase{}, err
	}
	state.PendingScores = map[string]string{pendingTraceID: "persisted-score-environment"}
	spec := memoryGateSpec{Name: "S100", TargetBytes: target, ExpectedScoreCalls: 1, ExpectedSourceTurns: turns + 1, ExpectedSelectedTurns: 1, ExpectedTraceID: pendingTraceID}
	return finishPreparedMemoryCase(root, state, spec)
}

func prepareCorruptSiblingCase(root string) (preparedMemoryCase, error) {
	prepared, err := prepareProcessedHistoryCase(root)
	if err != nil {
		return preparedMemoryCase{}, err
	}
	prepared.spec.Name = "C100"
	prepared.spec.CorruptSibling = true
	prepared.specPath = filepath.Join(root, "spec.json")
	if err := writeMemorySpec(prepared.specPath, prepared.spec); err != nil {
		return preparedMemoryCase{}, err
	}
	corruptPath := filepath.Join(prepared.root, "sessions", "memory", "rollout-z-corrupt.jsonl")
	if err := os.WriteFile(corruptPath, []byte("{not-json}\n"), 0o600); err != nil {
		return preparedMemoryCase{}, err
	}
	setMemoryMTime(corruptPath)
	return prepared, nil
}

func newMemoryState() exportstate.State {
	return exportstate.State{Version: exportstate.Version, ScanWatermarkNS: memoryGateNow().Add(-time.Minute).UnixNano()}
}

func createMemorySource(caseRoot, name string) (*os.File, string, error) {
	sessionDir := filepath.Join(caseRoot, "root", "sessions", "memory")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return nil, "", err
	}
	path := filepath.Join(sessionDir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", err
	}
	return file, path, nil
}

func writeMemoryHeader(writer *bufio.Writer) error {
	_, err := writer.WriteString("{\"timestamp\":\"2026-09-22T12:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":\"memory-session\"}}\n")
	return err
}

func memoryContextLine(prefix string, index int) string {
	return fmt.Sprintf(`{"timestamp":"2026-09-22T12:00:01Z","type":"turn_context","payload":{"turn_id":"%s-%09d","trace_id":"trace-%s-%09d"}}`, prefix, index, prefix, index)
}

func writeMemoryTurnContext(writer *bufio.Writer, prefix string, index int, traceID string) error {
	line := memoryContextLine(prefix, index)
	line = strings.Replace(line, fmt.Sprintf("trace-%s-%09d", prefix, index), traceID, 1)
	_, err := writer.WriteString(line + "\n")
	return err
}

func memoryCommentLine(message string) string {
	return fmt.Sprintf(`{"timestamp":"2026-09-22T12:00:02Z","type":"event_msg","payload":{"type":"agent_message","phase":"commentary","message":%q}}`, message)
}

func writeMemoryInputOutput(writer *bufio.Writer, input, output string) error {
	if _, err := fmt.Fprintf(writer, `{"timestamp":"2026-09-22T12:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}`+"\n", input); err != nil {
		return err
	}
	_, err := fmt.Fprintf(writer, `{"timestamp":"2026-09-22T12:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":%q}]}}`+"\n", output)
	return err
}

func writeMemoryComplete(writer *bufio.Writer) error {
	_, err := writer.WriteString("{\"timestamp\":\"2026-09-22T12:00:04Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}\n")
	return err
}

func finishMemorySource(file *os.File, writer *bufio.Writer, path string) error {
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return setMemoryMTime(path)
}

func setMemoryMTime(path string) error {
	stamp := memoryGateNow().Add(-time.Second)
	return os.Chtimes(path, stamp, stamp)
}

func memoryGateNow() time.Time { return time.Date(2026, 9, 22, 12, 1, 0, 0, time.UTC) }

func finishPreparedMemoryCase(root string, state exportstate.State, spec memoryGateSpec) (preparedMemoryCase, error) {
	prepared := preparedMemoryCase{
		root:     filepath.Join(root, "root"),
		state:    filepath.Join(root, "state-v3.json"),
		specPath: filepath.Join(root, "spec.json"),
		spec:     spec,
	}
	if err := exportstate.Save(context.Background(), prepared.state, state); err != nil {
		return preparedMemoryCase{}, err
	}
	if err := writeMemorySpec(prepared.specPath, spec); err != nil {
		return preparedMemoryCase{}, err
	}
	return prepared, nil
}

func writeMemorySpec(path string, spec memoryGateSpec) error {
	raw, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}
