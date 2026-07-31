package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/claudehook"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
	"github.com/kirilligum/codex-langfuse-tracer/internal/exportstate"
	"github.com/kirilligum/codex-langfuse-tracer/internal/langfuse"
	"github.com/kirilligum/codex-langfuse-tracer/internal/providers"
	"github.com/kirilligum/codex-langfuse-tracer/internal/watch"
)

type options struct {
	Provider              string
	ClaudeHook            bool
	SessionID             string
	Path                  string
	Latest                bool
	Watch                 bool
	Doctor                bool
	SyncModelPricing      bool
	TurnID                string
	ConfigPath            string
	StateFile             string
	Environment           string
	ServiceName           string
	PollIntervalSeconds   float64
	JSON                  bool
	Quiet                 bool
	NoVerify              bool
	VerifyWaitSeconds     float64
	VerifyIntervalSeconds float64
}

var syncModelPricing = langfuse.SyncModelPricing
var errUnsupportedProvider = providers.ErrUnsupportedProvider
var stdin io.Reader = os.Stdin

func (o options) Mode() string {
	switch {
	case o.SyncModelPricing:
		return "sync-model-pricing"
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
		Provider:              agenttrace.ProviderCodex,
		ConfigPath:            config.DefaultConfigPath(),
		StateFile:             config.DefaultStatePath(),
		Environment:           buildinfo.DefaultEnvironment,
		ServiceName:           buildinfo.DefaultServiceName,
		PollIntervalSeconds:   buildinfo.DefaultPollIntervalSeconds,
		VerifyWaitSeconds:     25.0,
		VerifyIntervalSeconds: 3.0,
	}

	fs := flag.NewFlagSet(buildinfo.InstalledBinaryName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.Provider, "provider", opts.Provider, "Trace provider: codex or claude")
	fs.BoolVar(&opts.ClaudeHook, "claude-hook", false, "Read a Claude Code hook payload from stdin and enqueue its transcript")
	fs.StringVar(&opts.SessionID, "session-id", "", "Codex session id from `codex resume <id>`")
	fs.StringVar(&opts.Path, "path", "", "Path to a Codex rollout JSONL file")
	fs.BoolVar(&opts.Latest, "latest", false, "Export the latest Codex rollout JSONL file")
	fs.BoolVar(&opts.Watch, "watch", false, "Continuously export newly completed Codex turns")
	fs.BoolVar(&opts.Doctor, "doctor", false, "Check Langfuse config, reachability, auth, service, and export state")
	fs.BoolVar(&opts.SyncModelPricing, "sync-model-pricing", false, "Create missing Langfuse model pricing definitions")
	fs.StringVar(&opts.TurnID, "turn-id", "", "Only export one turn id from the selected session")
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "Path to ~/.codex/config.toml")
	fs.StringVar(&opts.StateFile, "state-file", opts.StateFile, "Path to watch state file")
	fs.StringVar(&opts.Environment, "environment", opts.Environment, "Langfuse environment")
	fs.StringVar(&opts.ServiceName, "service-name", opts.ServiceName, "OTel service.name")
	fs.Float64Var(&opts.PollIntervalSeconds, "poll-interval-seconds", opts.PollIntervalSeconds, "Watch poll interval")
	fs.BoolVar(&opts.JSON, "json", false, "Emit machine-readable JSON for manual exports and doctor")
	fs.BoolVar(&opts.Quiet, "quiet", false, "Only print errors")
	fs.BoolVar(&opts.NoVerify, "no-verify", false, "Do not fetch traces after export")
	fs.Float64Var(&opts.VerifyWaitSeconds, "verify-wait-seconds", opts.VerifyWaitSeconds, "Trace verification timeout")
	fs.Float64Var(&opts.VerifyIntervalSeconds, "verify-interval-seconds", opts.VerifyIntervalSeconds, "Trace verification interval")
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
	for _, ok := range []bool{opts.SessionID != "", opts.Path != "", opts.Latest, opts.Watch, opts.Doctor, opts.SyncModelPricing, opts.ClaudeHook} {
		if ok {
			selected++
		}
	}
	if selected != 1 {
		return options{}, errors.New("exactly one source mode is required: --session-id, --path, --latest, --watch, --doctor, --claude-hook, or --sync-model-pricing")
	}
	if opts.JSON && (opts.Watch || opts.SyncModelPricing || opts.ClaudeHook) {
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
		enqueued, err := claudehook.Handle(stdin, opts.StateFile, time.Now())
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		if !opts.Quiet {
			fmt.Fprintf(stdout, "claude_hook enqueued=%v\n", enqueued)
		}
		return 0
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	if opts.SyncModelPricing {
		summary, err := syncModelPricing(ctx, cfg)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		if !opts.Quiet {
			fmt.Fprintf(stdout, "model_pricing existing=%d created=%d conflicting=%d\n", summary.Existing, summary.Created, summary.Conflicting)
		}
		return 0
	}
	if opts.Doctor {
		return runDoctor(ctx, cfg, opts, stdout, stderr)
	}
	if opts.Watch {
		err := watch.WatchSessions(ctx, watch.ScanOptions{
			Root:                config.CodexHome(),
			StatePath:           opts.StateFile,
			Stdout:              stdout,
			Stderr:              stderr,
			Quiet:               opts.Quiet,
			PollIntervalSeconds: opts.PollIntervalSeconds,
			ExportSpans: func(ctx context.Context, turn agenttrace.Turn, firstObservationIndex int, final bool) (int, error) {
				return langfuse.ExportSpans(ctx, cfg, turn, firstObservationIndex, final, opts.Environment, opts.ServiceName)
			},
			ExportScores: func(ctx context.Context, turn agenttrace.Turn) error {
				return langfuse.CreateDeterministicScores(ctx, cfg, turn, opts.Environment)
			},
		})
		if err != nil {
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
	projectID := ""
	for _, turn := range exportable {
		if !opts.JSON && !opts.Quiet {
			fmt.Fprintf(stdout, "turn=%s trace=%s input=%q output=%q observations=%d\n", turn.TurnID, turn.TraceID, preview(agenttrace.ExportText(turn.InputText())), preview(agenttrace.ExportText(turn.OutputText())), len(turn.Observations))
		}
		status, err := langfuse.ExportSpans(ctx, cfg, turn, 0, true, opts.Environment, opts.ServiceName)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		if err := langfuse.CreateDeterministicScores(ctx, cfg, turn, opts.Environment); err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		result := exportResult{
			Provider:    turn.Profile().Provider,
			SessionFile: sessionPath,
			TurnID:      turn.TurnID,
			TraceID:     turn.TraceID,
			Status:      status,
		}
		if !opts.JSON && !opts.Quiet {
			fmt.Fprintf(stdout, "exported trace=%s status=%d\n", turn.TraceID, status)
		}
		if !opts.NoVerify {
			verification, err := langfuse.VerifyTrace(ctx, cfg, turn, seconds(opts.VerifyWaitSeconds), seconds(opts.VerifyIntervalSeconds))
			if err != nil {
				fmt.Fprintf(stderr, "ERROR: %v\n", err)
				return 1
			}
			result.VerifiedInput = verification.HasInput
			result.VerifiedOutput = verification.HasOutput
			result.TraceURL = langfuse.TraceURLFromBody(cfg, turn.TraceID, verification.Body)
			if !opts.JSON && !opts.Quiet {
				fmt.Fprintf(stdout, "verified trace=%s input=%v output=%v\n", turn.TraceID, verification.HasInput, verification.HasOutput)
			}
			if !verification.HasInput || !verification.HasOutput {
				fmt.Fprintf(stderr, "ERROR: trace %s did not show exported input/output before timeout\n", turn.TraceID)
				return 1
			}
		}
		if result.TraceURL == "" {
			if projectID == "" {
				projectID, _ = langfuse.FetchProjectID(ctx, cfg)
			}
			result.TraceURL = langfuse.BuildTraceURL(cfg, projectID, turn.TraceID)
		}
		if opts.JSON {
			if err := writeJSONLine(stdout, result); err != nil {
				fmt.Fprintf(stderr, "ERROR: %v\n", err)
				return 1
			}
		} else if !opts.Quiet && result.TraceURL != "" {
			fmt.Fprintf(stdout, "trace_url=%s\n", result.TraceURL)
		}
	}
	return 0
}

type exportResult struct {
	Provider       string `json:"provider"`
	SessionFile    string `json:"session_file"`
	TurnID         string `json:"turn_id"`
	TraceID        string `json:"trace_id"`
	TraceURL       string `json:"trace_url,omitempty"`
	Status         int    `json:"status"`
	VerifiedInput  bool   `json:"verified_input,omitempty"`
	VerifiedOutput bool   `json:"verified_output,omitempty"`
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

func runDoctor(ctx context.Context, cfg config.LangfuseConfig, opts options, stdout, stderr io.Writer) int {
	result := doctorResult{OK: true}
	add := func(name, status, detail string) {
		result.Checks = append(result.Checks, doctorCheck{Name: name, Status: status, Detail: detail})
		if status == "fail" {
			result.OK = false
		}
	}

	add("config", "ok", "host="+cfg.Host)
	if status, err := langfuse.CheckHealth(ctx, cfg); err != nil {
		add("health", "fail", err.Error())
		if loopbackHostClosed(cfg.Host) {
			add("host_binding", "fail", "host points to loopback but the configured port is not accepting TCP connections")
		}
	} else {
		add("health", "ok", fmt.Sprintf("status=%d", status))
	}
	if status, err := langfuse.CheckAuth(ctx, cfg); err != nil {
		add("auth", "fail", err.Error())
	} else {
		add("auth", "ok", fmt.Sprintf("status=%d", status))
	}
	if projectID, err := langfuse.FetchProjectID(ctx, cfg); err != nil {
		add("project", "warn", err.Error())
	} else {
		add("project", "ok", "project_id="+projectID)
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
	}

	if output, err := runCommand(ctx, "systemctl", "--user", "is-active", buildinfo.InstalledServiceName); err != nil {
		add("watcher", "fail", strings.TrimSpace(string(output)))
	} else {
		add("watcher", "ok", strings.TrimSpace(string(output)))
	}
	if output, err := runCommand(ctx, "journalctl", "--user", "-u", buildinfo.InstalledServiceName, "--since", "15 minutes ago", "--no-pager"); err != nil {
		add("recent_errors", "warn", strings.TrimSpace(string(output)))
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

func loopbackHostClosed(rawHost string) bool {
	parsed, err := netURL(rawHost)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}
	port := parsed.Port()
	if port == "" {
		switch parsed.Scheme {
		case "https":
			port = "443"
		default:
			port = "80"
		}
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 500*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return false
	}
	return true
}

func netURL(rawHost string) (*url.URL, error) {
	if !strings.Contains(rawHost, "://") {
		rawHost = "http://" + rawHost
	}
	return url.Parse(rawHost)
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
		return "", errors.New("exactly one source mode is required: --session-id, --path, --latest, --watch, or --sync-model-pricing")
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
