package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/claudehook"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
	"github.com/kirilligum/codex-langfuse-tracer/internal/laminar"
	"github.com/kirilligum/codex-langfuse-tracer/internal/providers"
	"github.com/kirilligum/codex-langfuse-tracer/internal/watch"
)

type options struct {
	Provider            string
	ClaudeHook          bool
	SessionID           string
	Path                string
	Latest              bool
	Watch               bool
	Doctor              bool
	CheckReceiver       bool
	TurnID              string
	ConfigPath          string
	StateFile           string
	ServiceName         string
	PollIntervalSeconds float64
	JSON                bool
	Quiet               bool
}

var hostnameUserID = laminar.HostnameUserID
var errUnsupportedProvider = providers.ErrUnsupportedProvider
var stdin io.Reader = os.Stdin
var checkCollectorHealth = func(ctx context.Context) (int, error) {
	return checkHTTPStatus(ctx, "http://127.0.0.1:14317/")
}
var checkCollectorMetrics = func(ctx context.Context) (int, error) {
	return checkHTTPStatus(ctx, "http://127.0.0.1:14319/metrics")
}

func (o options) Mode() string {
	switch {
	case o.CheckReceiver:
		return "check-receiver"
	case o.ClaudeHook:
		return "claude-hook"
	case o.SessionID != "":
		return "session-id"
	case o.Path != "":
		return "path"
	case o.Latest:
		return "latest"
	case o.Watch:
		return "watch"
	case o.Doctor:
		return "doctor"
	default:
		return ""
	}
}

func parseArgs(args []string) (options, error) {
	opts := options{
		Provider:            agenttrace.ProviderCodex,
		ConfigPath:          config.DefaultLaminarConfigPath(),
		StateFile:           config.DefaultStatePath(),
		ServiceName:         buildinfo.DefaultServiceName,
		PollIntervalSeconds: buildinfo.DefaultPollIntervalSeconds,
	}

	fs := flag.NewFlagSet(buildinfo.InstalledBinaryName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.Provider, "provider", opts.Provider, "Trace provider: codex or claude")
	fs.BoolVar(&opts.ClaudeHook, "claude-hook", false, "Read a Claude Code hook payload from stdin and enqueue its transcript")
	fs.StringVar(&opts.SessionID, "session-id", "", "Codex session id from `codex resume <id>`")
	fs.StringVar(&opts.Path, "path", "", "Path to a Codex rollout JSONL file")
	fs.BoolVar(&opts.Latest, "latest", false, "Export the latest Codex rollout JSONL file")
	fs.BoolVar(&opts.Watch, "watch", false, "Continuously export newly completed Codex turns")
	fs.BoolVar(&opts.Doctor, "doctor", false, "Check Laminar receiver, Collector, watcher service, and export state")
	fs.BoolVar(&opts.CheckReceiver, "check-receiver", false, "Validate the local authenticated Laminar receiver without exporting a span")
	fs.StringVar(&opts.TurnID, "turn-id", "", "Only export one turn id from the selected session")
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "Path to ~/.config/lmnr/codex-tracer.json")
	fs.StringVar(&opts.StateFile, "state-file", opts.StateFile, "Path to watch state file")
	fs.StringVar(&opts.ServiceName, "service-name", opts.ServiceName, "OTel service.name")
	fs.Float64Var(&opts.PollIntervalSeconds, "poll-interval-seconds", opts.PollIntervalSeconds, "Watch poll interval")
	fs.BoolVar(&opts.JSON, "json", false, "Emit machine-readable JSON for manual exports and doctor")
	fs.BoolVar(&opts.Quiet, "quiet", false, "Only print errors")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	spec, err := providers.Get(opts.Provider)
	if err != nil {
		return options{}, err
	}
	opts.Provider = spec.Name

	selected := 0
	for _, ok := range []bool{opts.SessionID != "", opts.Path != "", opts.Latest, opts.Watch, opts.Doctor, opts.CheckReceiver, opts.ClaudeHook} {
		if ok {
			selected++
		}
	}
	if selected != 1 {
		return options{}, errors.New("exactly one source mode is required: --session-id, --path, --latest, --watch, --doctor, --check-receiver, or --claude-hook")
	}
	if opts.JSON && (opts.Watch || opts.CheckReceiver || opts.ClaudeHook) {
		return options{}, errors.New("--json is supported only for manual exports and --doctor")
	}
	if spec.ExplicitPathOnly && opts.Path == "" {
		return options{}, fmt.Errorf("%s provider supports only --path in this release", spec.DisplayName)
	}
	return opts, nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	if opts.ClaudeHook {
		enqueued, err := claudehook.Handle(ctx, stdin, opts.StateFile, time.Now())
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		if !opts.Quiet {
			fmt.Fprintf(stdout, "claude_hook enqueued=%v\n", enqueued)
		}
		return 0
	}
	cfg, err := config.LoadLaminar(opts.ConfigPath)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	if opts.CheckReceiver {
		status, err := laminar.CheckReceiver(ctx, cfg)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		if !opts.Quiet {
			fmt.Fprintf(stdout, "receiver_accepted status=%d\n", status)
		}
		return 0
	}
	if opts.Doctor {
		return runDoctor(ctx, cfg, opts, stdout, stderr)
	}
	userID, err := hostnameUserID()
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	if opts.Watch {
		// Only the daemon needs a graceful, successful signal shutdown. Hooks
		// retain normal signal termination even while blocked reading stdin.
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		err := watch.WatchSessions(ctx, watch.ScanOptions{
			Root:                config.CodexHome(),
			StatePath:           opts.StateFile,
			Stdout:              stdout,
			Stderr:              stderr,
			Quiet:               opts.Quiet,
			PollIntervalSeconds: opts.PollIntervalSeconds,
			ResolveWorkspace:    laminar.ResolveWorkspace,
			ExportSpans: func(ctx context.Context, turn agenttrace.Turn, environment string) (int, error) {
				return laminar.ExportSpans(ctx, cfg, turn, environment, userID, opts.ServiceName)
			},
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return 0
			}
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		return 0
	}

	sessionPath, err := selectedSessionPath(opts)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	turns, err := parseProviderTurns(opts.Provider, sessionPath)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	if opts.TurnID != "" {
		filtered := turns[:0]
		for _, turn := range turns {
			if turn.TurnID == opts.TurnID {
				filtered = append(filtered, turn)
			}
		}
		turns = filtered
	}
	exportable := agenttrace.ExportableTurns(turns)
	if len(exportable) == 0 {
		if !opts.Quiet {
			fmt.Fprintf(stderr, "No completed %s turns with visible input/output found in %s\n", providers.DisplayName(opts.Provider), sessionPath)
		}
		return 1
	}
	if !opts.JSON && !opts.Quiet {
		fmt.Fprintf(stdout, "session_file=%s\n", sessionPath)
	}
	for _, turn := range exportable {
		resolvedTurn, environment, err := laminar.ResolveWorkspace(ctx, turn)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		turn = resolvedTurn
		if !opts.JSON && !opts.Quiet {
			fmt.Fprintf(stdout, "turn=%s trace=%s input=%q output=%q observations=%d\n", turn.TurnID, turn.TraceID, preview(agenttrace.ExportText(turn.InputText())), preview(agenttrace.ExportText(turn.OutputText())), len(turn.Observations))
		}
		status, err := laminar.ExportSpans(ctx, cfg, turn, environment, userID, opts.ServiceName)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		result := exportResult{
			Provider:        turn.Profile().Provider,
			SessionFile:     sessionPath,
			TurnID:          turn.TurnID,
			TraceID:         turn.TraceID,
			CollectorStatus: status,
		}
		if !opts.JSON && !opts.Quiet {
			fmt.Fprintf(stdout, "collector_accepted trace=%s status=%d\n", turn.TraceID, status)
		}
		if opts.JSON {
			if err := writeJSONLine(stdout, result); err != nil {
				fmt.Fprintf(stderr, "ERROR: %v\n", err)
				return 1
			}
		}
	}
	return 0
}

type exportResult struct {
	Provider        string `json:"provider"`
	SessionFile     string `json:"session_file"`
	TurnID          string `json:"turn_id"`
	TraceID         string `json:"trace_id"`
	CollectorStatus int    `json:"collector_status"`
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type doctorResult struct {
	OK     bool          `json:"ok"`
	Checks []doctorCheck `json:"checks"`
}

var runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func runDoctor(ctx context.Context, cfg config.LaminarConfig, opts options, stdout, stderr io.Writer) int {
	result := doctorResult{OK: true}
	add := func(name, status, detail string) {
		result.Checks = append(result.Checks, doctorCheck{Name: name, Status: status, Detail: detail})
		if status == "fail" {
			result.OK = false
		}
	}

	add("config", "ok", "receiver="+cfg.BaseURL)
	if status, err := laminar.CheckReceiver(ctx, cfg); err != nil {
		add("receiver_auth", "fail", err.Error())
	} else {
		add("receiver_auth", "ok", fmt.Sprintf("status=%d", status))
	}
	if status, err := checkCollectorHealth(ctx); err != nil {
		add("collector_health", "fail", err.Error())
	} else {
		add("collector_health", "ok", fmt.Sprintf("status=%d", status))
	}
	if status, err := checkCollectorMetrics(ctx); err != nil {
		add("collector_metrics", "fail", err.Error())
	} else {
		add("collector_metrics", "ok", fmt.Sprintf("status=%d", status))
	}

	if state, err := exportstate.Load(opts.StateFile); err != nil {
		add("state", "fail", err.Error())
	} else if state == nil {
		add("state", "warn", "state file does not exist yet")
	} else {
		add("state", "ok", fmt.Sprintf("queue=%d processed=%d", len(state.Queue), len(state.ProcessedTraceIDs)))
		if len(state.Queue) > 0 {
			add("state_queue", "fail", fmt.Sprintf("queue=%d", len(state.Queue)))
		}
		if len(state.PendingScores) > 0 {
			add("state_legacy_pending_traces", "fail", fmt.Sprintf("pending_traces=%d", len(state.PendingScores)))
		}
	}

	if output, err := runCommand(ctx, "systemctl", "--user", "is-active", buildinfo.InstalledServiceName); err != nil {
		add("watcher", "fail", strings.TrimSpace(string(output)))
	} else {
		add("watcher", "ok", strings.TrimSpace(string(output)))
	}
	if output, err := runCommand(ctx, "journalctl", "--user", "-u", buildinfo.InstalledServiceName, "--since", "15 minutes ago", "--no-pager"); err != nil {
		add("recent_errors", "fail", "journal unavailable: "+strings.TrimSpace(string(output)))
	} else {
		count := recentErrorCount(string(output))
		status := "ok"
		if count > 0 {
			status = "fail"
		}
		add("recent_errors", status, fmt.Sprintf("count=%d", count))
	}

	if opts.JSON {
		if err := writeJSONLine(stdout, result); err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
	} else {
		for _, check := range result.Checks {
			if check.Detail != "" {
				fmt.Fprintf(stdout, "doctor %s %s %s\n", check.Name, check.Status, check.Detail)
			} else {
				fmt.Fprintf(stdout, "doctor %s %s\n", check.Name, check.Status)
			}
		}
		if result.OK {
			fmt.Fprintln(stdout, "doctor result ok")
		} else {
			fmt.Fprintln(stdout, "doctor result failed")
		}
	}
	if !result.OK {
		return 1
	}
	return 0
}

func writeJSONLine(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(writer, string(encoded))
	return err
}

func checkHTTPStatus(ctx context.Context, endpoint string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return response.StatusCode, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return response.StatusCode, nil
}

func recentErrorCount(logs string) int {
	count := 0
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "ERROR:") || strings.Contains(line, "connect: connection refused") || strings.Contains(line, "failed to export") {
			count++
		}
	}
	return count
}

func selectedSessionPath(opts options) (string, error) {
	switch {
	case opts.Path != "":
		return opts.Path, nil
	case opts.Latest:
		return codextrace.LatestSession(config.CodexHome())
	case opts.SessionID != "":
		return codextrace.FindSessionByID(opts.SessionID, config.CodexHome())
	default:
		return "", errors.New("exactly one source mode is required: --session-id, --path, --latest, --watch, --doctor, --check-receiver, or --claude-hook")
	}
}

func parseProviderTurns(provider, path string) ([]agenttrace.Turn, error) {
	return providers.ParseTurns(provider, path)
}

func preview(value string) string {
	value = strings.ReplaceAll(value, "\n", "\\n")
	if len(value) <= 120 {
		return value
	}
	return value[:117] + "..."
}

func seconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
