// Package logs shows dusk's service log without making the user type
// journalctl: it runs `journalctl --user -u dusk.service` with sane defaults.
package logs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// Unit is the systemd unit whose log is shown.
const Unit = "dusk.service"

// DefaultLines is how many recent lines are shown when -n is not given.
const DefaultLines = 50

// Exec runs journalctl with args, wiring its output to stdout and stderr.
type Exec func(ctx context.Context, args []string, stdout, stderr io.Writer) error

// Options select what to show.
type Options struct {
	Follow bool   // keep printing new lines until interrupted
	Lines  int    // number of recent lines (0 with Follow shows only new lines)
	Since  string // optional journalctl time spec, for example "1h ago" or "today"
}

// Args builds the journalctl arguments. Values are attached with "=" so a value
// that starts with "-" can never be read as another option.
func Args(o Options) ([]string, error) {
	if o.Lines < 0 {
		return nil, fmt.Errorf("the number of lines cannot be negative: %d", o.Lines)
	}
	args := []string{"--user", "--unit=" + Unit, "--no-pager", fmt.Sprintf("--lines=%d", o.Lines)}
	if o.Since != "" {
		args = append(args, "--since="+o.Since)
	}
	if o.Follow {
		args = append(args, "--follow")
	}
	return args, nil
}

// Show prints the log. It returns nil when the user interrupts a --follow.
func Show(ctx context.Context, run Exec, o Options, stdout, stderr io.Writer) error {
	args, err := Args(o)
	if err != nil {
		return err
	}
	if err := run(ctx, args, stdout, stderr); err != nil {
		if ctx.Err() != nil {
			return nil // Ctrl+C while following
		}
		return err
	}
	return nil
}

// ExecJournalctl is the real Exec.
func ExecJournalctl(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrNotFound) {
		return errors.New("journalctl not found in PATH: `dusk logs` needs systemd. " +
			"If you started dusk with `dusk run`, its log is printed in that terminal")
	}
	return err
}
