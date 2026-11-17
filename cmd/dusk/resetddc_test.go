// Filename: cmd/dusk/resetddc_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dusk/internal/ddc/ddctest"
	"dusk/internal/state"
)

func resetEnv(t *testing.T, input string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	old := stdin
	stdin = strings.NewReader(input)
	t.Cleanup(func() { stdin = old })
}

// writableRunner is fakeRunner that also accepts setvcp (the tests count the writes).
func writableRunner(t *testing.T) *ddctest.FakeRunner {
	t.Helper()
	fr := fakeRunner(t)
	inner := fr.Handler
	fr.Handler = func(a []string) (string, error) {
		if a[0] == "setvcp" {
			return "", nil
		}
		return inner(a)
	}
	return fr
}

func TestResetDDCWithoutConfirmationWritesNothing(t *testing.T) {
	resetEnv(t, "\n\n\n") // Enter = no
	fr := writableRunner(t)
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"reset-ddc", "--to-max"}, fr, &out, &errb); code != exitOK {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if n := len(fr.SetCalls()); n != 0 {
		t.Fatalf("%d writes without confirmation", n)
	}
	if !strings.Contains(out.String(), "skipped") || !strings.Contains(out.String(), "R 40  G 40  B 40") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestResetDDCRestoresOnlyWhatWasConfirmed(t *testing.T) {
	resetEnv(t, "y\nn\nn\n")
	fr := writableRunner(t)
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"reset-ddc", "--to-max"}, fr, &out, &errb); code != exitOK {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	sets := fr.SetCalls()
	if len(sets) != 3 {
		t.Fatalf("writes = %v", sets)
	}
	for _, c := range sets {
		switch c[1] {
		case "16", "18", "1A":
		default:
			t.Fatalf("wrote VCP %s", c[1])
		}
	}
}

func TestResetDDCYesUsesTheSavedOriginalAndForgetsIt(t *testing.T) {
	resetEnv(t, "")
	path, _ := state.DefaultPath()
	st := state.New()
	st.Originals["24G4#SN-AOC-0001"] = state.RGB{R: 90, G: 95, B: 100}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (state.Store{Path: path}).Save(st); err != nil {
		t.Fatal(err)
	}
	fr := writableRunner(t)
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"reset-ddc", "--yes"}, fr, &out, &errb); code != exitOK {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "R 90  G 95  B 100 (the value saved by an older dusk)") {
		t.Errorf("output:\n%s", out.String())
	}
	got, _ := (state.Store{Path: path}).Load()
	if _, ok := got.Originals["24G4#SN-AOC-0001"]; ok {
		t.Error("the saved original should be removed after restoring")
	}
}

func TestCheckConfigShowsWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	toml := "[night]\nstart = \"21:00\"\nend = \"07:00\"\nfallback = \"auto\"\n[[monitor]]\nname = \"a\"\nmatch = \"A\"\npoints = [{ time = \"08:00\", brightness = 50 }]\n"
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"check-config", path}, nil, &out, &errb); code != exitOK {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "warning: night.fallback is ignored") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestResetDDCWithoutAnOriginalLeavesTheMonitorAlone(t *testing.T) {
	resetEnv(t, "y\ny\ny\n")
	fr := writableRunner(t)
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"reset-ddc"}, fr, &out, &errb); code != exitOK {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if n := len(fr.SetCalls()); n != 0 {
		t.Fatalf("%d writes without a saved original", n)
	}
	if !strings.Contains(out.String(), "no original saved by an older dusk") || !strings.Contains(out.String(), "--to-max") {
		t.Errorf("output:\n%s", out.String())
	}
}
