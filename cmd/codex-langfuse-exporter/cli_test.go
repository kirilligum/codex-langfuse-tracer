package main

import (
	"strings"
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
)

// TEST-002
func TestCLIFlags(t *testing.T) {
	t.Parallel()

	opts, err := parseArgs([]string{"--latest"})
	if err != nil {
		t.Fatalf("parse latest: %v", err)
	}
	if !opts.Latest || opts.Mode() != "latest" {
		t.Fatalf("latest mode not selected: %+v", opts)
	}
	if opts.Environment != buildinfo.DefaultEnvironment {
		t.Fatalf("environment = %q, want %q", opts.Environment, buildinfo.DefaultEnvironment)
	}
	if opts.ServiceName != buildinfo.DefaultServiceName {
		t.Fatalf("service name = %q, want %q", opts.ServiceName, buildinfo.DefaultServiceName)
	}
	if opts.PollIntervalSeconds != buildinfo.DefaultPollIntervalSeconds {
		t.Fatalf("poll interval = %v", opts.PollIntervalSeconds)
	}
	if opts.VerifyWaitSeconds != 25.0 || opts.VerifyIntervalSeconds != 3.0 {
		t.Fatalf("verify defaults = %v/%v", opts.VerifyWaitSeconds, opts.VerifyIntervalSeconds)
	}

	opts, err = parseArgs([]string{
		"--path", "/tmp/rollout.jsonl",
		"--turn-id", "turn-1",
		"--no-verify",
		"--verify-wait-seconds", "1.5",
		"--verify-interval-seconds", "0.25",
		"--quiet",
	})
	if err != nil {
		t.Fatalf("parse path mode: %v", err)
	}
	if opts.Path != "/tmp/rollout.jsonl" || opts.TurnID != "turn-1" || !opts.NoVerify || !opts.Quiet {
		t.Fatalf("path options not preserved: %+v", opts)
	}
	if opts.VerifyWaitSeconds != 1.5 || opts.VerifyIntervalSeconds != 0.25 {
		t.Fatalf("verify values not preserved: %+v", opts)
	}

	for _, args := range [][]string{
		{},
		{"--latest", "--watch"},
		{"--latest", "--session-id", "abc"},
		{"--path", "a", "--session-id", "abc"},
	} {
		_, err := parseArgs(args)
		if err == nil {
			t.Fatalf("parseArgs(%v) succeeded, want error", args)
		}
		if !strings.Contains(err.Error(), "exactly one source mode") {
			t.Fatalf("parseArgs(%v) error = %q", args, err)
		}
	}
}

// EVAL-001
func TestEvalBuildAndPackageGraph(t *testing.T) {
	t.Parallel()
	opts, err := parseArgs([]string{"--watch"})
	if err != nil {
		t.Fatalf("parse watch: %v", err)
	}
	if opts.Mode() != "watch" {
		t.Fatalf("mode = %q", opts.Mode())
	}
}
