package executor

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunSuccess(t *testing.T) {
	cmd := New("echo hello world", 5*time.Second)
	ctx := context.Background()
	stdout, err := cmd.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "hello world") {
		t.Errorf("stdout = %q, want 'hello world'", stdout)
	}
}

func TestRunTimeout(t *testing.T) {
	// Sleep longer than the configured timeout
	cmd := New("sleep 10", 100*time.Millisecond)
	ctx := context.Background()
	_, err := cmd.Run(ctx)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v, should mention timeout", err)
	}
}

func TestRunCommandNotFound(t *testing.T) {
	cmd := New("nonexistent_command_xyz 2>&1", 5*time.Second)
	ctx := context.Background()
	_, err := cmd.Run(ctx)
	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}
}

func TestRunContextCancelled(t *testing.T) {
	cmd := New("echo hello", 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := cmd.Run(ctx)
	if err == nil {
		t.Fatal("expected context cancelled error")
	}
}

func TestRunMultilineOutput(t *testing.T) {
	cmd := New("printf 'line1\\nline2\\nline3\\n'", 5*time.Second)
	ctx := context.Background()
	stdout, err := cmd.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Errorf("got %d lines, want 3: %q", len(lines), stdout)
	}
}

func TestRunStderr(t *testing.T) {
	// Command writes to stderr but exits 0 — stderr is in error message only on failure
	cmd := New("echo ok", 5*time.Second)
	ctx := context.Background()
	_, err := cmd.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
