package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOfficialPluginCaptureUsesSourceHeaderAndRejectsUnreviewedBundle(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "hook.cjs")
	source := []byte(`const fs=require('fs');const p=JSON.parse(fs.readFileSync(0,'utf8'));if(p.session_id!=='source-session'||p.hook_event_name!=='Watch')process.exit(1);`)
	if err := os.WriteFile(bundle, source, 0600); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dir, "rollout.jsonl")
	if err := os.WriteFile(rollout, []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"source-session\"}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := options{CodexPluginHook: bundle, CodexPluginHookSHA256: fmt.Sprintf("%x", sha256.Sum256(source))}
	if err := officialPluginCapture(opts)(context.Background(), rollout, "wrong-fork-id"); err != nil {
		t.Fatal(err)
	}
	opts.CodexPluginHookSHA256 = "wrong"
	if err := officialPluginCapture(opts)(context.Background(), rollout, ""); err == nil {
		t.Fatal("changed bundle executed")
	}
}
