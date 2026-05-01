package main

import (
	"testing"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
)

// TEST-001
func TestMainPackageCompiles(t *testing.T) {
	t.Parallel()
	if buildinfo.InstalledBinaryName != "codex-langfuse-exporter" {
		t.Fatalf("unexpected binary name %q", buildinfo.InstalledBinaryName)
	}
}
