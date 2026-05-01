package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
	"github.com/kirilligum/codex-langfuse-tracer/internal/codextrace"
	"github.com/kirilligum/codex-langfuse-tracer/internal/config"
	"github.com/kirilligum/codex-langfuse-tracer/internal/langfuse"
	"github.com/kirilligum/codex-langfuse-tracer/internal/watch"
)

type options struct {
	SessionID             string
	Path                  string
	Latest                bool
	Watch                 bool
	TurnID                string
	ConfigPath            string
	StateFile             string
	Environment           string
	ServiceName           string
	PollIntervalSeconds   float64
	Quiet                 bool
	NoVerify              bool
	VerifyWaitSeconds     float64
	VerifyIntervalSeconds float64
}

func (o options) Mode() string {
	switch {
	case o.SessionID != "":
		return "session-id"
	case o.Path != "":
		return "path"
	case o.Latest:
		return "latest"
	case o.Watch:
		return "watch"
	default:
		return ""
	}
}

func parseArgs(args []string) (options, error) {
	opts := options{
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
	fs.StringVar(&opts.SessionID, "session-id", "", "Codex session id from `codex resume <id>`")
	fs.StringVar(&opts.Path, "path", "", "Path to a Codex rollout JSONL file")
	fs.BoolVar(&opts.Latest, "latest", false, "Export the latest Codex rollout JSONL file")
	fs.BoolVar(&opts.Watch, "watch", false, "Continuously export newly completed Codex turns")
	fs.StringVar(&opts.TurnID, "turn-id", "", "Only export one turn id from the selected session")
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "Path to ~/.codex/config.toml")
	fs.StringVar(&opts.StateFile, "state-file", opts.StateFile, "Path to watch state file")
	fs.StringVar(&opts.Environment, "environment", opts.Environment, "Langfuse environment")
	fs.StringVar(&opts.ServiceName, "service-name", opts.ServiceName, "OTel service.name")
	fs.Float64Var(&opts.PollIntervalSeconds, "poll-interval-seconds", opts.PollIntervalSeconds, "Watch poll interval")
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

	selected := 0
	for _, ok := range []bool{opts.SessionID != "", opts.Path != "", opts.Latest, opts.Watch} {
		if ok {
			selected++
		}
	}
	if selected != 1 {
		return options{}, errors.New("exactly one source mode is required: --session-id, --path, --latest, or --watch")
	}
	return opts, nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	if opts.Watch {
		err := watch.WatchSessions(ctx, watch.ScanOptions{
			Root:                config.CodexHome(),
			StatePath:           opts.StateFile,
			Stdout:              stdout,
			Stderr:              stderr,
			Quiet:               opts.Quiet,
			PollIntervalSeconds: opts.PollIntervalSeconds,
			Export: func(ctx context.Context, turn codextrace.Turn) (int, error) {
				return langfuse.ExportTurn(ctx, cfg, turn, opts.Environment, opts.ServiceName)
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
	turns, err := codextrace.ParseTurns(sessionPath)
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
	exportable := codextrace.ExportableTurns(turns)
	if len(exportable) == 0 {
		if !opts.Quiet {
			fmt.Fprintf(stderr, "No completed Codex turns with visible input/output found in %s\n", sessionPath)
		}
		return 1
	}
	if !opts.Quiet {
		fmt.Fprintf(stdout, "session_file=%s\n", sessionPath)
	}
	for _, turn := range exportable {
		if !opts.Quiet {
			fmt.Fprintf(stdout, "turn=%s trace=%s input=%q output=%q observations=%d\n", turn.TurnID, turn.TraceID, preview(codextrace.ExportText(turn.InputText())), preview(codextrace.ExportText(turn.OutputText())), len(turn.Observations))
		}
		status, err := langfuse.ExportTurn(ctx, cfg, turn, opts.Environment, opts.ServiceName)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		if !opts.Quiet {
			fmt.Fprintf(stdout, "exported trace=%s status=%d\n", turn.TraceID, status)
		}
		if !opts.NoVerify {
			hasInput, hasOutput, err := langfuse.VerifyTraceIO(ctx, cfg, turn, seconds(opts.VerifyWaitSeconds), seconds(opts.VerifyIntervalSeconds))
			if err != nil {
				fmt.Fprintf(stderr, "ERROR: %v\n", err)
				return 1
			}
			if !opts.Quiet {
				fmt.Fprintf(stdout, "verified trace=%s input=%v output=%v\n", turn.TraceID, hasInput, hasOutput)
			}
			if !hasInput || !hasOutput {
				fmt.Fprintf(stderr, "ERROR: trace %s did not show exported input/output before timeout\n", turn.TraceID)
				return 1
			}
		}
	}
	return 0
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
		return "", errors.New("exactly one source mode is required: --session-id, --path, --latest, or --watch")
	}
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
