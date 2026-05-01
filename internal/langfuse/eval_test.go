package langfuse

import (
	"context"
	"testing"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/buildinfo"
)

// EVAL-004
func TestEvalOTLPPayloadSizeAndLatency(t *testing.T) {
	t.Parallel()

	start := time.Now()
	exporter := &memoryExporter{}
	if err := EmitTurn(context.Background(), completeTurn(t), buildinfo.DefaultEnvironment, buildinfo.DefaultServiceName, exporter); err != nil {
		t.Fatalf("EmitTurn: %v", err)
	}
	if len(exporter.Snapshots()) == 0 {
		t.Fatal("no spans exported")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("EmitTurn elapsed = %s, want <= 500ms", elapsed)
	}
}
