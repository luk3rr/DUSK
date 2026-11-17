// Filename: internal/service/service_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"dusk/internal/ddc/ddctest"
	"dusk/internal/service"
)

func TestState(t *testing.T) {
	unit := filepath.Join(t.TempDir(), "dusk.service")
	active := "active\n"
	fail := error(nil)
	r := &ddctest.FakeRunner{Handler: func(a []string) (string, error) { return active, fail }}
	m := service.Manager{Systemctl: r, UnitPath: unit}
	if s := m.State(context.Background()); s.Installed || !s.Active {
		t.Fatalf("state = %+v", s)
	}
	if err := os.WriteFile(unit, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	active, fail = "inactive\n", errors.New("exit status 3")
	if s := m.State(context.Background()); !s.Installed || s.Active {
		t.Fatalf("state = %+v", s)
	}
}

func TestStartAndStopCallSystemctlUser(t *testing.T) {
	r := &ddctest.FakeRunner{Handler: func([]string) (string, error) { return "", nil }}
	m := service.Manager{Systemctl: r}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := r.Calls()
	if len(calls) != 2 || calls[0][1] != "start" || calls[1][1] != "stop" || calls[0][0] != "--user" {
		t.Fatalf("calls = %v", calls)
	}
}

func TestStartFailureIsReported(t *testing.T) {
	r := &ddctest.FakeRunner{Handler: func([]string) (string, error) { return "", errors.New("unit not found") }}
	if err := (service.Manager{Systemctl: r}).Start(context.Background()); err == nil {
		t.Fatal("want error")
	}
}
