package ddc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ExecRunner runs a real external binary (ddcutil by default).
type ExecRunner struct {
	Path    string        // default: "ddcutil"
	Timeout time.Duration // default: 15s per call
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, args ...string) (string, error) {
	path, timeout := r.Path, r.Timeout
	if path == "" {
		path = "ddcutil"
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			msg := fmt.Sprintf("%s not found in PATH", path)
			if path == "ddcutil" {
				msg += "; install it with your package manager (for example: sudo apt install ddcutil)"
			}
			return "", errors.New(msg)
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s %s: %w", path, strings.Join(args, " "), ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("%s %s: %w: %s", path, strings.Join(args, " "), err, msg)
	}
	return stdout.String(), nil
}
