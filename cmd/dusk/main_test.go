package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dusk/internal/daemon"
	"dusk/internal/ddc/ddctest"
	"dusk/internal/gamma"
	"dusk/internal/gamma/gammatest"
	"dusk/internal/ipc"
)

// fakeSetupEnv replaces the seams so install, uninstall and start never touch the real machine.
func fakeSetupEnv(t *testing.T, input string) {
	t.Helper()
	oldIn, oldSys, oldExe := stdin, systemctl, executable
	stdin = strings.NewReader(input)
	systemctl = &ddctest.FakeRunner{Handler: func([]string) (string, error) { return "", nil }}
	t.Cleanup(func() { stdin, systemctl, executable = oldIn, oldSys, oldExe })
}

func fakeRunner(t *testing.T) *ddctest.FakeRunner {
	t.Helper()
	detect, err := os.ReadFile("../../internal/ddc/testdata/detect_brief.txt")
	if err != nil {
		t.Fatal(err)
	}
	return &ddctest.FakeRunner{Handler: func(a []string) (string, error) {
		switch a[0] {
		case "--version":
			return "ddcutil 2.2.5\n", nil
		case "detect":
			return string(detect), nil
		case "getvcp":
			return "VCP " + a[1] + " C 40 100\n", nil
		}
		return "", errors.New("unexpected call")
	}}
}

func TestListReadOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no config
	fr := fakeRunner(t)
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"list"}, fr, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"24G4", "LG ULTRAWIDE", "LF24T450F", "40/100", "24G4#SN-AOC-0001", "yes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if len(fr.SetCalls()) != 0 {
		t.Error("list never writes")
	}
}

func TestListShowsNameFromConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "dusk"), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "[[monitor]]\nname = \"desk\"\nmatch = \"LG ULTRAWIDE\"\npoints = [{ time = \"08:00\", brightness = 50 }]\n"
	if err := os.WriteFile(filepath.Join(dir, "dusk", "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"list"}, fakeRunner(t), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "desk") {
		t.Errorf("name missing:\n%s", out.String())
	}
}

func TestListDetectFails(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	fr := &ddctest.FakeRunner{Handler: func([]string) (string, error) { return "", errors.New("no ddcutil") }}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"list"}, fr, &out, &errb); code != 1 {
		t.Errorf("exit %d", code)
	}
}

func TestCheckConfigAndUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte("[[monitor]]\nname=\"a\"\nmatch=\"A\"\npoints=[{time=\"25:00\",brightness=1}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"check-config", path}, nil, &out, &errb); code != 1 {
		t.Errorf("an invalid config must exit 1, got %d", code)
	}
	if code := run(context.Background(), nil, nil, &out, &errb); code != 1 {
		t.Errorf("no arguments must exit 1, got %d", code)
	}
	if code := run(context.Background(), []string{"xyz"}, nil, &out, &errb); code != 1 {
		t.Errorf("an unknown command must exit 1, got %d", code)
	}
}

// withDaemon starts a daemon against fake hardware and points XDG_RUNTIME_DIR at its socket.
func withDaemon(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "dusk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	cfg := filepath.Join(dir, "config.toml")
	toml := "poll_interval = \"1h\"\n[[monitor]]\nname = \"desk\"\nmatch = \"24G4\"\npoints = [{ time = \"00:00\", brightness = 100 }]\n"
	if err := os.WriteFile(cfg, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		daemon.Run(ctx, daemon.Params{
			ConfigPath: cfg, SocketPath: filepath.Join(dir, "dusk.sock"),
			DDC: fakeRunnerWithOneMonitor(), Gamma: oneScreen(),
		})
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})
	sock := filepath.Join(dir, "dusk.sock")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := ipc.Send(context.Background(), sock, ipc.Request{Cmd: ipc.CmdStatus}); err == nil && r.OK && len(r.Monitors) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon did not come up")
}

func oneScreen() gamma.Backend {
	return gammatest.New(gamma.Output{CRTC: 0, Name: "DP-1", Product: "24G4", Serial: "SN1"})
}

func fakeRunnerWithOneMonitor() *ddctest.FakeRunner {
	return &ddctest.FakeRunner{Handler: func(a []string) (string, error) {
		switch a[0] {
		case "detect":
			return "Display 1\n   I2C bus: /dev/i2c-5\n   DRM connector: card1-DP-1\n   Monitor: AOC:24G4:SN1\n", nil
		case "getvcp":
			return "VCP " + a[1] + " C 40 100\n", nil
		}
		return "", errors.New("unexpected call")
	}}
}

func TestControlCommandsAgainstARunningDaemon(t *testing.T) {
	withDaemon(t)
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"status"}, nil, &out, &errb); code != 0 {
		t.Fatalf("status exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"running", "config:", "night mode:  off", "desk", "24G4#SN1", "online", "brightness   100% now, target 100%", "R 100%  G 100%  B 100%", "schedule     00:00 100%"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status output lacks %q:\n%s", want, out.String())
		}
	}
	for _, cmd := range []string{"pause", "resume", "reload"} {
		out.Reset()
		if code := run(context.Background(), []string{cmd}, nil, &out, &errb); code != 0 || !strings.Contains(out.String(), "ok") {
			t.Errorf("%s: exit %d, out %q, err %q", cmd, code, out.String(), errb.String())
		}
	}
	out.Reset()
	run(context.Background(), []string{"pause"}, nil, &out, &errb)
	out.Reset()
	run(context.Background(), []string{"status"}, nil, &out, &errb)
	if !strings.Contains(out.String(), "state:       paused") {
		t.Errorf("status after pause:\n%s", out.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"stop"}, nil, &out, &errb); code != 0 {
		t.Errorf("stop exit %d: %s", code, errb.String())
	}
}

func TestControlCommandsWithoutADaemonExitTwo(t *testing.T) {
	dir, err := os.MkdirTemp("", "dusk")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("XDG_RUNTIME_DIR", dir)
	for _, cmd := range []string{"status", "pause", "resume", "reload", "stop"} {
		var out, errb bytes.Buffer
		if code := run(context.Background(), []string{cmd}, nil, &out, &errb); code != 2 {
			t.Errorf("%s: exit %d; want 2", cmd, code)
		}
		if !strings.Contains(errb.String(), "not running") {
			t.Errorf("%s: stderr %q", cmd, errb.String())
		}
	}
}

func TestControlWithoutRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"status"}, nil, &out, &errb); code != 1 {
		t.Errorf("exit %d; want 1", code)
	}
}

func TestRunWithoutConfigExplains(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"run"}, nil, &out, &errb); code != 1 {
		t.Errorf("exit %d; want 1", code)
	}
	if !strings.Contains(errb.String(), "configuration file not found") {
		t.Errorf("stderr %q", errb.String())
	}
}

func fakeBinary(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "dusk")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInstallAndUninstallCommands(t *testing.T) {
	fakeSetupEnv(t, "")
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	bin := fakeBinary(t)
	executable = func() (string, error) { return bin, nil }
	sys := systemctl.(*ddctest.FakeRunner)

	var out, errb bytes.Buffer
	// Without a configuration, install explains how to create one and changes nothing.
	if code := run(context.Background(), []string{"install", "--yes"}, fakeRunner(t), &out, &errb); code != 1 || !strings.Contains(errb.String(), "create the configuration first") {
		t.Fatalf("exit %d\n%s\n%s", code, out.String(), errb.String())
	}
	if len(sys.Calls()) != 0 {
		t.Fatalf("systemctl must not run without a configuration: %v", sys.Calls())
	}
	cfgPath := filepath.Join(cfgHome, "dusk", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "[[monitor]]\nname = \"a\"\nmatch = \"A\"\npoints = [{ time = \"08:00\", brightness = 50 }]\n"
	if err := os.WriteFile(cfgPath, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := run(context.Background(), []string{"install", "--yes"}, fakeRunner(t), &out, &errb); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out.String(), errb.String())
	}
	unit, err := os.ReadFile(filepath.Join(cfgHome, "systemd", "user", "dusk.service"))
	resolved, _ := filepath.EvalSymlinks(bin)
	if err != nil || !strings.Contains(string(unit), "ExecStart="+resolved+" run\n") {
		t.Errorf("unit: %v\n%s", err, unit)
	}
	if len(sys.Calls()) != 2 {
		t.Errorf("systemctl calls = %v", sys.Calls())
	}

	out.Reset()
	if code := run(context.Background(), []string{"uninstall", "--yes"}, nil, &out, &errb); code != 0 {
		t.Fatalf("uninstall exit %d: %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(cfgHome, "systemd", "user", "dusk.service")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the unit should be removed")
	}
	if _, err := os.Stat(filepath.Join(cfgHome, "dusk", "config.toml")); err != nil {
		t.Error("the config must survive an uninstall")
	}
}

func TestInstallRefusesATemporaryBuild(t *testing.T) {
	fakeSetupEnv(t, "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tmp := filepath.Join(t.TempDir(), "go-build42", "b001", "exe", "dusk")
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	executable = func() (string, error) { return tmp, nil }
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"install", "--yes"}, fakeRunner(t), &out, &errb); code != 1 {
		t.Errorf("exit %d; want 1", code)
	}
	if !strings.Contains(errb.String(), "just install") {
		t.Errorf("stderr %q", errb.String())
	}
	if n := len(systemctl.(*ddctest.FakeRunner).Calls()); n != 0 {
		t.Errorf("systemctl must not be called: %d", n)
	}
}

func TestInstallWithAnExplicitConfigFlag(t *testing.T) {
	fakeSetupEnv(t, "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bin := fakeBinary(t)
	executable = func() (string, error) { return bin, nil }
	custom := filepath.Join(t.TempDir(), "mine.toml")
	if err := os.WriteFile(custom, []byte("[[monitor]]\nname=\"a\"\nmatch=\"A\"\npoints=[{time=\"08:00\",brightness=1}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"install", "--yes", "--no-start", "--config", custom}, fakeRunner(t), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	cfgDir, _ := os.UserConfigDir()
	unit, _ := os.ReadFile(filepath.Join(cfgDir, "systemd", "user", "dusk.service"))
	if !strings.Contains(string(unit), "run --config "+custom) {
		t.Errorf("unit:\n%s", unit)
	}
	calls := systemctl.(*ddctest.FakeRunner).Calls()
	if got := calls[len(calls)-1]; strings.Join(got, " ") != "--user enable dusk.service" {
		t.Errorf("last call = %v", got)
	}
}

func TestLogsCommand(t *testing.T) {
	old := journalctl
	t.Cleanup(func() { journalctl = old })
	var got []string
	journalctl = func(_ context.Context, args []string, stdout, _ io.Writer) error {
		got = args
		io.WriteString(stdout, "line from the journal\n")
		return nil
	}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"logs"}, nil, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if strings.Join(got, " ") != "--user --unit=dusk.service --no-pager --lines=50" || out.String() != "line from the journal\n" {
		t.Errorf("args=%v out=%q", got, out.String())
	}
	if code := run(context.Background(), []string{"logs", "-f", "-n", "5", "--since", "today"}, nil, &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Join(got, " ") != "--user --unit=dusk.service --no-pager --lines=5 --since=today --follow" {
		t.Errorf("args = %v", got)
	}
	if code := run(context.Background(), []string{"logs", "--lines=-1"}, nil, &out, &errb); code != 1 {
		t.Errorf("a negative count must exit 1, got %d", code)
	}
	if code := run(context.Background(), []string{"logs", "--bogus"}, nil, &out, &errb); code != 1 {
		t.Errorf("an unknown flag must exit 1, got %d", code)
	}
	journalctl = func(context.Context, []string, io.Writer, io.Writer) error { return errors.New("exit status 1") }
	if code := run(context.Background(), []string{"logs"}, nil, &out, &errb); code != 1 || !strings.Contains(errb.String(), "dusk logs:") {
		t.Errorf("a journalctl failure must exit 1 with a message: %d %q", code, errb.String())
	}
}

func TestStartCommand(t *testing.T) {
	fakeSetupEnv(t, "")
	dir, err := os.MkdirTemp("", "dusk")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("XDG_RUNTIME_DIR", dir)
	oldWait := startWait
	startWait = 300 * time.Millisecond
	t.Cleanup(func() { startWait = oldWait })
	sys := systemctl.(*ddctest.FakeRunner)

	// Not running and systemd starts nothing: the command must say so.
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"start"}, nil, &out, &errb); code != 1 {
		t.Errorf("exit %d; want 1 when the daemon never answers", code)
	}
	if got := sys.Calls(); len(got) != 1 || strings.Join(got[0], " ") != "--user start dusk.service" {
		t.Errorf("systemctl calls = %v", got)
	}
	if !strings.Contains(errb.String(), "dusk logs") {
		t.Errorf("stderr %q", errb.String())
	}

	// systemctl failing (unit not installed) explains how to install.
	sys.Handler = func([]string) (string, error) { return "", errors.New("Unit dusk.service not found") }
	errb.Reset()
	if code := run(context.Background(), []string{"start"}, nil, &out, &errb); code != 1 || !strings.Contains(errb.String(), "dusk install") {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}

	// Already running: nothing is started again.
	withDaemon(t)
	before := len(sys.Calls())
	out.Reset()
	if code := run(context.Background(), []string{"start"}, nil, &out, &errb); code != 0 || !strings.Contains(out.String(), "already running") {
		t.Errorf("exit %d, out %q", code, out.String())
	}
	if len(sys.Calls()) != before {
		t.Error("an already running daemon must not be started again")
	}
}

func TestStartReportsSuccessWhenTheDaemonComesUp(t *testing.T) {
	fakeSetupEnv(t, "")
	dir, err := os.MkdirTemp("", "dusk")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("XDG_RUNTIME_DIR", dir)
	// "systemctl start" brings up a real daemon (against fake hardware).
	systemctl.(*ddctest.FakeRunner).Handler = func([]string) (string, error) {
		cfg := filepath.Join(dir, "config.toml")
		os.WriteFile(cfg, []byte("poll_interval = \"1h\"\n[[monitor]]\nname = \"d\"\nmatch = \"24G4\"\npoints = [{ time = \"00:00\", brightness = 100 }]\n"), 0o600)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go daemon.Run(ctx, daemon.Params{ConfigPath: cfg, SocketPath: filepath.Join(dir, "dusk.sock"),
			DDC: fakeRunnerWithOneMonitor(), Gamma: oneScreen()})
		return "", nil
	}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"start"}, nil, &out, &errb); code != 0 || !strings.Contains(out.String(), "dusk started") {
		t.Errorf("exit %d, out %q, err %q", code, out.String(), errb.String())
	}
}

func TestPrintStatusDuringNightMode(t *testing.T) {
	st := &ipc.Status{
		Night: 0.5, Config: "/home/x/.config/dusk/config.toml", PollInterval: "30s", RescanInterval: "5m", VerifyInterval: "5m",
		NightMode: ipc.NightStatus{Enabled: true, Start: "19:00", End: "07:00", Ramp: "1h", Phase: "fading in", NextChange: "20:00"},
		Warnings:  []string{"another program keeps changing the colors of DP-1 (is GNOME Night Light on?)"},
		Monitors: []ipc.MonitorStatus{
			{Name: "desk", Key: "MODEL#S1", Output: "DP-1", Bus: 5, DDC: true, BrightnessBackend: "software", Floor: 10, Current: 30, Target: 31, Scale: 37, Online: true,
				Gamma: ipc.RGB{R: 100, G: 100, B: 80}, NightLevel: ipc.RGB{R: 100, G: 100, B: 60},
				Schedule: []ipc.SchedulePoint{{Time: "08:00", Brightness: 70}, {Time: "22:00", Brightness: 25}}},
			{Name: "side", Key: "OTHER", Output: "HDMI-1", BrightnessBackend: "ddc", Current: 20, Target: 20, Online: false, Fails: 3, LastError: "i2c hung",
				NightLevel: ipc.RGB{R: 100, G: 100, B: 40}, OwnLevel: true, Schedule: []ipc.SchedulePoint{{Time: "00:00", Brightness: 50}}},
		},
	}
	var out bytes.Buffer
	printStatus(&out, st)
	for _, want := range []string{
		"state:       running",
		"night mode:  on, 19:00 -> 07:00, fade 1h",
		"now:         fading in (50%), next change at 20:00",
		"warning:     another program keeps changing the colors of DP-1",
		"desk  (MODEL#S1, DP-1, DDC bus 5)  online",
		"brightness   30% now, target 31% (software, floor 10%, screen scale 37%)",
		"color        R 100%  G 100%  B 80% (software)",
		"night level  R 100%  G 100%  B 60% at the peak (global level)",
		"schedule     08:00 70%, 22:00 25%",
		"side  (OTHER, HDMI-1, no DDC)  offline",
		"(monitor backlight over DDC)",
		"night level  R 100%  G 100%  B 40% at the peak (own level)",
		"last error   i2c hung (3 failures)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}
}

func TestPrintStatusWhenPausedAndNightIsOff(t *testing.T) {
	var out bytes.Buffer
	printStatus(&out, &ipc.Status{Paused: true})
	for _, want := range []string{"state:       paused", "night mode:  off", "no screens under a schedule yet"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}
}
