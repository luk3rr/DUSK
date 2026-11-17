// Filename: cmd/dusk/tui_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"dusk/internal/tui"
)

func withTUI(t *testing.T, tty bool, fn func(context.Context, tui.Deps) error) {
	t.Helper()
	oldI, oldR := interactive, tuiRun
	interactive = func() bool { return tty }
	tuiRun = fn
	t.Cleanup(func() { interactive, tuiRun = oldI, oldR })
}

func TestNoArgumentsOpensTheTUIInATerminal(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	opened := false
	withTUI(t, true, func(_ context.Context, d tui.Deps) error {
		opened = true
		if _, ok := d.Client.(tui.IPCClient); !ok || d.Config == nil || d.Screens == nil {
			t.Errorf("deps = %+v", d)
		}
		return nil
	})
	var out, errb bytes.Buffer
	if code := run(context.Background(), nil, nil, &out, &errb); code != exitOK || !opened {
		t.Fatalf("code %d opened %v: %s", code, opened, errb.String())
	}
}

func TestNoArgumentsWithoutATerminalPrintsUsage(t *testing.T) {
	withTUI(t, false, func(context.Context, tui.Deps) error {
		t.Fatal("the TUI must not start without a terminal")
		return nil
	})
	var out, errb bytes.Buffer
	if code := run(context.Background(), nil, nil, &out, &errb); code != exitUsage || !strings.Contains(errb.String(), "usage: dusk") {
		t.Fatalf("code %d: %s", code, errb.String())
	}
}

func TestTUIFailureIsReported(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	withTUI(t, true, func(context.Context, tui.Deps) error { return errors.New("no tty") })
	var out, errb bytes.Buffer
	if code := run(context.Background(), nil, nil, &out, &errb); code != exitUsage || !strings.Contains(errb.String(), "no tty") {
		t.Fatalf("code %d: %s", code, errb.String())
	}
}
