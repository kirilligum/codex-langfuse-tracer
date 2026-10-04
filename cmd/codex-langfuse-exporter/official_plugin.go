package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

func officialPluginCapture(opts options) func(context.Context, string, string) error {
	if opts.CodexPluginHook == "" {
		return nil
	}
	return func(ctx context.Context, path, sessionID string) error {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		line, err := bufio.NewReader(file).ReadBytes('\n')
		file.Close()
		if err != nil {
			return err
		}
		var header struct {
			Payload struct {
				ID string `json:"id"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(line, &header); err != nil {
			return err
		}
		sessionID = header.Payload.ID
		if sessionID == "" {
			return fmt.Errorf("rollout header has no session id")
		}
		bundle, err := os.ReadFile(opts.CodexPluginHook)
		if err != nil {
			return err
		}
		if opts.CodexPluginHookSHA256 == "" || fmt.Sprintf("%x", sha256.Sum256(bundle)) != opts.CodexPluginHookSHA256 {
			return fmt.Errorf("official plugin bundle differs from the configured SHA256")
		}
		payload, err := json.Marshal(map[string]string{"hook_event_name": "Watch", "session_id": sessionID, "transcript_path": path})
		if err != nil {
			return err
		}
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(callCtx, "node", opts.CodexPluginHook)
		cmd.Stdin = bytes.NewReader(payload)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("official plugin live export failed: %w", err)
		}
		return nil
	}
}
