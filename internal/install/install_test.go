package install_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dusk/internal/ddc/ddctest"
	"dusk/internal/install"
	"dusk/internal/prompt"
)

type rig struct {
	t    *testing.T
	sys  *ddctest.FakeRunner
	opts install.Options
	out  bytes.Buffer
	dir  string
}

func newRig(t *testing.T, withConfig bool) *rig {
	t.Helper()
	dir := t.TempDir()
	r := &rig{t: t, dir: dir}
	r.sys = &ddctest.FakeRunner{Handler: func([]string) (string, error) { return "", nil }}
	r.opts = install.Options{
		Bin:        "/home/someone/.local/bin/dusk",
		ConfigPath: filepath.Join(dir, "config", "dusk", "config.toml"),
		UnitDir:    filepath.Join(dir, "config", "systemd", "user"),
		Systemctl:  r.sys,
		Out:        &r.out,
	}
	if withConfig {
		if err := os.MkdirAll(filepath.Dir(r.opts.ConfigPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(r.opts.ConfigPath, []byte("# config\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// writeConfig does what the real wizard does: create the directory and the file.
func writeConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("# made by setup\n"), 0o644)
}

func (r *rig) install(input string) error {
	r.out.Reset()
	r.opts.In = strings.NewReader(input)
	return install.Install(context.Background(), r.opts)
}

func (r *rig) calls() [][]string { return r.sys.Calls() }

func (r *rig) unit() string {
	b, err := os.ReadFile(r.opts.UnitPath())
	if err != nil {
		return ""
	}
	return string(b)
}

func TestRenderUnit(t *testing.T) {
	u := install.RenderUnit("/usr/local/bin/dusk", "")
	for _, want := range []string{
		"After=graphical-session.target", "PartOf=graphical-session.target",
		"ExecStart=/usr/local/bin/dusk run\n", "Restart=on-failure", "RestartSec=5", "WantedBy=graphical-session.target",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q:\n%s", want, u)
		}
	}
	if strings.Contains(u, "--config") {
		t.Error("the default config must not be passed explicitly")
	}
	if got := install.RenderUnit("/opt/my tools/dusk", "/home/a b/50%/c.toml"); !strings.Contains(got,
		`ExecStart="/opt/my tools/dusk" run --config "/home/a b/50%%/c.toml"`) {
		t.Errorf("quoting:\n%s", got)
	}
}

func TestResolveBin(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dusk")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(real)
	if got, err := install.ResolveBin(link, ""); err != nil || got != want {
		t.Errorf("symlink: %q %v", got, err)
	}
	if got, err := install.ResolveBin("/ignored", real); err != nil || got != want {
		t.Errorf("override: %q %v", got, err)
	}
	if _, err := install.ResolveBin("", ""); err == nil {
		t.Error("an unknown executable must be an error")
	}
	if _, err := install.ResolveBin(filepath.Join(dir, "missing"), ""); err == nil {
		t.Error("a missing file must be an error")
	}
	if _, err := install.ResolveBin(dir, ""); err == nil {
		t.Error("a directory must be an error")
	}
	tmpBuild := filepath.Join(dir, "go-build123", "b001", "exe")
	if err := os.MkdirAll(filepath.Dir(tmpBuild), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpBuild, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := install.ResolveBin(tmpBuild, ""); err == nil || !strings.Contains(err.Error(), "just install") {
		t.Errorf("a go run build must be refused with advice: %v", err)
	}
}

func TestInstallHappyPath(t *testing.T) {
	r := newRig(t, true)
	if err := r.install("y\n"); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	want := [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", "dusk.service"}}
	if !reflect.DeepEqual(r.calls(), want) {
		t.Errorf("systemctl calls = %v", r.calls())
	}
	if !strings.Contains(r.unit(), "ExecStart=/home/someone/.local/bin/dusk run\n") {
		t.Errorf("unit:\n%s", r.unit())
	}
	info, _ := os.Stat(r.opts.UnitPath())
	if info.Mode().Perm() != 0o644 {
		t.Errorf("unit mode = %v", info.Mode().Perm())
	}
	for _, want := range []string{"systemctl --user daemon-reload", "dusk status", "dusk logs -f", "dusk uninstall"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, r.out.String())
		}
	}
}

func TestInstallPassesAnExplicitConfig(t *testing.T) {
	r := newRig(t, true)
	r.opts.ExplicitConfig = true
	if err := r.install("\n"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.unit(), "run --config "+r.opts.ConfigPath) {
		t.Errorf("unit:\n%s", r.unit())
	}
}

func TestInstallNoStartOnlyEnables(t *testing.T) {
	r := newRig(t, true)
	r.opts.NoStart = true
	if err := r.install(""); err == nil {
		// without Yes and without input the confirmation cannot be answered
		t.Fatal("expected the missing confirmation to abort")
	}
	r.opts.Yes = true
	if err := r.install(""); err != nil {
		t.Fatal(err)
	}
	last := r.calls()[len(r.calls())-1]
	if !reflect.DeepEqual(last, []string{"--user", "enable", "dusk.service"}) {
		t.Errorf("last call = %v", last)
	}
	if !strings.Contains(r.out.String(), "systemctl --user start dusk.service") {
		t.Errorf("output:\n%s", r.out.String())
	}
}

func TestDecliningChangesNothing(t *testing.T) {
	r := newRig(t, true)
	err := r.install("n\n")
	if !errors.Is(err, install.ErrDeclined) {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls()) != 0 || r.unit() != "" {
		t.Errorf("nothing may change: calls=%v unit=%q", r.calls(), r.unit())
	}
	if _, err := os.Stat(r.opts.UnitDir); !errors.Is(err, os.ErrNotExist) {
		t.Error("the unit directory must not even be created")
	}
}

func TestInstallRunsSetupWhenThereIsNoConfig(t *testing.T) {
	r := newRig(t, false)
	ran := false
	r.opts.RunSetup = func(_ context.Context, p *prompt.Prompter) error {
		ran = true
		// the wizard reads from the same stream as install
		if v, err := p.String("A question from the wizard", "", nil); err != nil || v != "typed" {
			t.Errorf("shared input: %q %v", v, err)
		}
		return writeConfig(r.opts.ConfigPath)
	}
	// y: run the wizard; typed: the wizard's answer; y: confirm the installation
	if err := r.install("y\ntyped\ny\n"); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	if !ran || r.unit() == "" || len(r.calls()) != 2 {
		t.Errorf("ran=%v unit=%q calls=%v", ran, r.unit(), r.calls())
	}
}

func TestDecliningTheSetupAbortsWithoutChanges(t *testing.T) {
	r := newRig(t, false)
	r.opts.RunSetup = func(context.Context, *prompt.Prompter) error {
		t.Error("the wizard must not run")
		return nil
	}
	if err := r.install("n\n"); !errors.Is(err, install.ErrDeclined) {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls()) != 0 || r.unit() != "" {
		t.Error("nothing may change")
	}
}

func TestSetupFailureAborts(t *testing.T) {
	r := newRig(t, false)
	boom := errors.New("no monitors")
	r.opts.RunSetup = func(context.Context, *prompt.Prompter) error { return boom }
	if err := r.install("y\n"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if r.unit() != "" || len(r.calls()) != 0 {
		t.Error("nothing may change after a failed setup")
	}
}

func TestMissingConfigWithoutAWizard(t *testing.T) {
	r := newRig(t, false)
	if err := r.install("y\n"); err == nil || !strings.Contains(err.Error(), "dusk setup") {
		t.Errorf("err = %v", err)
	}
}

func TestYesNeedsNoInput(t *testing.T) {
	r := newRig(t, false)
	r.opts.Yes = true
	r.opts.RunSetup = func(_ context.Context, _ *prompt.Prompter) error {
		return writeConfig(r.opts.ConfigPath)
	}
	if err := r.install(""); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	if len(r.calls()) != 2 {
		t.Errorf("calls = %v", r.calls())
	}
}

func TestSystemctlFailuresExplainThemselves(t *testing.T) {
	r := newRig(t, true)
	r.sys.Handler = func([]string) (string, error) {
		return "", errors.New("Failed to connect to bus: No medium found")
	}
	err := r.install("y\n")
	if err == nil || !strings.Contains(err.Error(), "the unit was written") || !strings.Contains(err.Error(), "systemctl --user status") {
		t.Fatalf("err = %v", err)
	}
	if r.unit() == "" {
		t.Error("the unit file should be reported as written")
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	r := newRig(t, true)
	for i := 0; i < 2; i++ {
		if err := r.install("y\n"); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if len(r.calls()) != 4 {
		t.Errorf("calls = %v", r.calls())
	}
	entries, _ := os.ReadDir(r.opts.UnitDir)
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestOnlyUserScopeIsEverUsed(t *testing.T) {
	r := newRig(t, true)
	if err := r.install("y\n"); err != nil {
		t.Fatal(err)
	}
	r.opts.In = strings.NewReader("y\n")
	if err := install.Uninstall(context.Background(), r.opts, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.calls() {
		if c[0] != "--user" {
			t.Errorf("a system-wide systemctl call: %v", c)
		}
	}
}

func TestUninstall(t *testing.T) {
	r := newRig(t, true)
	if err := r.install("y\n"); err != nil {
		t.Fatal(err)
	}
	r.out.Reset()
	r.sys = &ddctest.FakeRunner{Handler: func([]string) (string, error) { return "", nil }}
	r.opts.Systemctl = r.sys
	r.opts.In = strings.NewReader("y\n")
	if err := install.Uninstall(context.Background(), r.opts, []string{r.opts.ConfigPath}); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	want := [][]string{{"--user", "disable", "--now", "dusk.service"}, {"--user", "daemon-reload"}}
	if !reflect.DeepEqual(r.calls(), want) {
		t.Errorf("calls = %v", r.calls())
	}
	if r.unit() != "" {
		t.Error("the unit file should be gone")
	}
	if _, err := os.Stat(r.opts.ConfigPath); err != nil {
		t.Error("the configuration must be kept")
	}
	if !strings.Contains(r.out.String(), r.opts.ConfigPath) {
		t.Errorf("the kept files should be listed:\n%s", r.out.String())
	}
}

func TestUninstallWhenNothingIsInstalled(t *testing.T) {
	r := newRig(t, true)
	r.sys.Handler = func(a []string) (string, error) {
		if a[1] == "disable" {
			return "", errors.New("Unit file dusk.service does not exist")
		}
		return "", nil
	}
	r.opts.In = strings.NewReader("y\n")
	if err := install.Uninstall(context.Background(), r.opts, nil); err != nil {
		t.Fatalf("an absent service is not an error: %v", err)
	}
	if !strings.Contains(r.out.String(), "could not disable") {
		t.Errorf("a note is expected:\n%s", r.out.String())
	}
}

func TestUninstallDeclined(t *testing.T) {
	r := newRig(t, true)
	if err := r.install("y\n"); err != nil {
		t.Fatal(err)
	}
	before := len(r.calls())
	r.opts.In = strings.NewReader("n\n")
	if err := install.Uninstall(context.Background(), r.opts, nil); !errors.Is(err, install.ErrDeclined) {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls()) != before || r.unit() == "" {
		t.Error("declining must change nothing")
	}
}
