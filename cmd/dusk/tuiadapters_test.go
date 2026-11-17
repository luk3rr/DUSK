// Filename: cmd/dusk/tuiadapters_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dusk/internal/ddc/ddctest"
	"dusk/internal/logs"
)

func writeConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "dusk", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "[[monitor]]\nname = \"a\"\nmatch = \"A\"\npoints = [{ time = \"08:00\", brightness = 50 }]\n"
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCLIServiceInstallAndUninstall(t *testing.T) {
	fakeSetupEnv(t, "")
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	cfg := writeConfig(t, cfgHome)
	bin := fakeBinary(t)
	executable = func() (string, error) { return bin, nil }
	sys := systemctl.(*ddctest.FakeRunner)
	svc := cliService{configPath: cfg}

	out, err := svc.Install(context.Background())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "enabled and running") {
		t.Errorf("out = %q", out)
	}
	if st := svc.State(context.Background()); !st.Installed {
		t.Errorf("state = %+v", st)
	}
	if len(sys.Calls()) < 2 {
		t.Errorf("systemctl calls = %v", sys.Calls())
	}
	if _, err := svc.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := svc.State(context.Background()); st.Installed {
		t.Errorf("the unit should be gone: %+v", st)
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Error("the config must survive an uninstall")
	}
}

func TestCLIServiceInstallWithoutAConfigDoesNothing(t *testing.T) {
	fakeSetupEnv(t, "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	executable = func() (string, error) { return fakeBinary(t), nil }
	svc := cliService{configPath: filepath.Join(t.TempDir(), "missing.toml")}
	if _, err := svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "create the configuration first") {
		t.Fatalf("err = %v", err)
	}
	if n := len(systemctl.(*ddctest.FakeRunner).Calls()); n != 0 {
		t.Errorf("systemctl ran %d times", n)
	}
}

func TestCLIServiceLogs(t *testing.T) {
	old := journalctl
	journalctl = func(_ context.Context, args []string, stdout, _ io.Writer) error {
		if !strings.Contains(strings.Join(args, " "), "--lines=7") {
			t.Errorf("args = %v", args)
		}
		_, err := io.WriteString(stdout, "one\ntwo\nthree\n")
		return err
	}
	t.Cleanup(func() { journalctl = old })
	lines, err := cliService{}.Logs(context.Background(), 7)
	if err != nil || strings.Join(lines, ",") != "one,two,three" {
		t.Fatalf("lines %v err %v", lines, err)
	}
	var _ logs.Exec = journalctl
}

func TestCLIResetPlansWithoutWritingAndAppliesOnlyWhatIsAllowed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fr := writableRunner(t)
	r := cliReset{runner: fr}
	items, err := r.Plan(context.Background(), false)
	if err != nil || len(items) != 3 {
		t.Fatalf("items %v err %v", items, err)
	}
	for _, it := range items {
		if !it.NoOriginal {
			t.Errorf("%s: with no saved original nothing may be assumed", it.Key)
		}
	}
	if len(fr.SetCalls()) != 0 {
		t.Fatal("Plan must not write")
	}
	if err := r.Apply(context.Background(), items[0].Key, false); err == nil {
		t.Fatal("Apply must refuse a monitor without a saved original")
	}
	if len(fr.SetCalls()) != 0 {
		t.Fatal("a refused Apply wrote")
	}
	if err := r.Apply(context.Background(), items[0].Key, true); err != nil {
		t.Fatal(err)
	}
	for _, c := range fr.SetCalls() {
		if c[1] != "16" && c[1] != "18" && c[1] != "1A" {
			t.Fatalf("wrote VCP %s", c[1])
		}
	}
	if n := len(fr.SetCalls()); n != 3 {
		t.Fatalf("writes = %d", n)
	}
	if err := r.Apply(context.Background(), "nope", true); err == nil {
		t.Error("an unknown monitor must be an error")
	}
}
