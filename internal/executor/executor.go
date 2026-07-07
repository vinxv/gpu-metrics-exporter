package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Command wraps an smi command execution with timeout and mutual exclusion.
// Only one command runs at a time to avoid hammering the SMI tool.
type Command struct {
	shellCmd string
	timeout  time.Duration
	mu       sync.Mutex
}

// New creates a new Command.
// shellCmd is the full command string executed via `sh -c`.
func New(shellCmd string, timeout time.Duration) *Command {
	return &Command{
		shellCmd: shellCmd,
		timeout:  timeout,
	}
}

// stderrBufToStr returns the stderr buffer content, or falls back to combined
// if stderr is empty (in case the tool writes everything to stdout).
func stderrBufToStr(errBuf, combined *bytes.Buffer) string {
	if errBuf.Len() > 0 {
		return errBuf.String()
	}
	return combined.String()
}

// Run executes the command and returns its combined stdout+stderr.
// ctx is the caller's context (e.g. HTTP request context).
// The actual execution timeout is min(ctx deadline, configured timeout).
//
// Only one Run can be in progress at a time per Command instance —
// concurrent callers block until the current execution completes.
func (c *Command) Run(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if the caller already gave up while we waited for the lock.
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("context cancelled before execution: %w", err)
	}

	execCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", c.shellCmd)

	var combined, errBuf bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = io.MultiWriter(&combined, &errBuf)

	err := cmd.Run()

	if execCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("command timed out after %v: %s", c.timeout, errBuf.String())
	}

	if err != nil {
		return "", fmt.Errorf("command failed (exit=%v): %s", err, stderrBufToStr(&errBuf, &combined))
	}

	return combined.String(), nil
}
