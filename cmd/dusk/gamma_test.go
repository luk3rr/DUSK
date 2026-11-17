// Filename: cmd/dusk/gamma_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"dusk/internal/ddc/ddctest"
	"dusk/internal/gamma"
	"dusk/internal/gamma/gammatest"
)

// TestMain keeps every test away from the real D-Bus: by default there is no backend.
func TestMain(m *testing.M) {
	gammaConnect = func(context.Context) (gamma.Backend, error) {
		return nil, fmt.Errorf("%w: disabled in tests", gamma.ErrNoBackend)
	}
	os.Exit(m.Run())
}

func withGamma(t *testing.T, b gamma.Backend, err error) {
	t.Helper()
	old := gammaConnect
	gammaConnect = func(context.Context) (gamma.Backend, error) { return b, err }
	t.Cleanup(func() { gammaConnect = old })
}

func TestResetGamma(t *testing.T) {
	f := gammatest.New(gamma.Output{CRTC: 0, Name: "DP-1"}, gamma.Output{CRTC: 1, Name: "HDMI-A-1"})
	for _, o := range f.Outs {
		_ = f.Set(context.Background(), o, gamma.Build(gammatest.Size, 1, 1, 0.3, 0.5))
	}
	withGamma(t, f, nil)
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"reset-gamma"}, &ddctest.FakeRunner{}, &out, &errb); code != exitOK {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	for _, o := range f.Outs {
		r, _ := f.Get(context.Background(), o)
		if !r.Equal(gamma.Identity(gammatest.Size)) {
			t.Errorf("%s not reset", o.Name)
		}
	}
	if !strings.Contains(out.String(), "DP-1: reset") || !strings.Contains(out.String(), "HDMI-A-1: reset") {
		t.Errorf("stdout = %q", out.String())
	}
	// Idempotent.
	if code := run(context.Background(), []string{"reset-gamma"}, &ddctest.FakeRunner{}, &out, &errb); code != exitOK {
		t.Fatalf("second run code = %d", code)
	}
}

func TestResetGammaNoBackend(t *testing.T) {
	withGamma(t, nil, errors.Join(gamma.ErrNoBackend, errors.New("no session")))
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"reset-gamma"}, &ddctest.FakeRunner{}, &out, &errb); code != exitUsage {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(errb.String(), "cannot reset colors") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestResetGammaContinuesAfterAFailure(t *testing.T) {
	f := gammatest.New(gamma.Output{CRTC: 0, Name: "DP-1"}, gamma.Output{CRTC: 1, Name: "DP-3"})
	f.FailSet[0] = errors.New("boom")
	withGamma(t, f, nil)
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"reset-gamma"}, &ddctest.FakeRunner{}, &out, &errb); code != exitUsage {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), "DP-3: reset") || !strings.Contains(errb.String(), "DP-1") {
		t.Errorf("out=%q err=%q", out.String(), errb.String())
	}
}

func TestListShowsScreensWithAndWithoutDDC(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	f := gammatest.New(
		gamma.Output{CRTC: 0, Name: "DP-1", Product: "24G4", Serial: "SN-AOC-0001"},
		gamma.Output{CRTC: 1, Name: "DP-9", Product: "NoDDC Panel"},
	)
	withGamma(t, f, nil)
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"list"}, fakeRunner(t), &out, &errb); code != exitOK {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	for _, want := range []string{"SCREEN", "DP-1", "DP-9", "NoDDC Panel", "24G4#SN-AOC-0001", "BRIGHTNESS"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestListWorksWithoutDDC(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withGamma(t, gammatest.New(gamma.Output{CRTC: 0, Name: "DP-1", Product: "X"}), nil)
	fr := &ddctest.FakeRunner{Handler: func([]string) (string, error) { return "", errors.New("ddcutil: not found") }}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"list"}, fr, &out, &errb); code != exitOK {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "DP-1") || !strings.Contains(errb.String(), "DDC/CI monitors unavailable") {
		t.Errorf("out=%q err=%q", out.String(), errb.String())
	}
}
