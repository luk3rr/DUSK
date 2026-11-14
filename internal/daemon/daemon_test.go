package daemon_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dusk/internal/daemon"
	"dusk/internal/ddc/ddctest"
	"dusk/internal/ipc"
)

const goodConfig = `
poll_interval = "1h"

[[monitor]]
name = "desk"
match = "24G4"
points = [{ time = "00:00", brightness = 50 }]
`

type harness struct {
	t      *testing.T
	sock   string
	config string
	ddc    *ddctest.FakeRunner
	done   chan error
	cancel context.CancelFunc
}

// start runs the daemon against fake hardware: the monitor already sits at its
// target, so nothing is ever written.
func start(t *testing.T) *harness {
	t.Helper()
	dir, err := os.MkdirTemp("", "dusk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	h := &harness{t: t, sock: filepath.Join(dir, "d.sock"), config: filepath.Join(dir, "config.toml")}
	if err := os.WriteFile(h.config, []byte(goodConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	h.ddc = &ddctest.FakeRunner{Handler: func(a []string) (string, error) {
		switch a[0] {
		case "detect":
			return "Display 1\n   I2C bus: /dev/i2c-5\n   DRM connector: card1-DP-1\n   Monitor: AOC:24G4:SN1\n", nil
		case "getvcp":
			return "VCP " + a[1] + " C 50 100\n", nil
		}
		return "", errors.New("unexpected call: " + a[0])
	}}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan error, 1)
	go func() {
		h.done <- daemon.Run(ctx, daemon.Params{
			ConfigPath: h.config, SocketPath: h.sock, StatePath: filepath.Join(dir, "state.json"), DDC: h.ddc,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(3 * time.Second):
		}
	})
	h.waitFor(func(r ipc.Response) bool { return r.OK && len(r.Monitors) == 1 }, "the first tick to publish a status")
	return h
}

func (h *harness) send(cmd string) ipc.Response {
	h.t.Helper()
	r, err := ipc.Send(context.Background(), h.sock, ipc.Request{Cmd: cmd})
	if err != nil {
		h.t.Fatalf("%s: %v", cmd, err)
	}
	return r
}

func (h *harness) waitFor(cond func(ipc.Response) bool, what string) ipc.Response {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := ipc.Send(context.Background(), h.sock, ipc.Request{Cmd: ipc.CmdStatus}); err == nil && cond(r) {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s", what)
	return ipc.Response{}
}

func TestStatusPauseResume(t *testing.T) {
	h := start(t)
	r := h.send(ipc.CmdStatus)
	m := r.Monitors[0]
	if r.Paused || m.Name != "desk" || m.Key != "24G4#SN1" || m.Bus != 5 || m.Current != 50 || m.Target != 50 || !m.Online {
		t.Errorf("status = %+v", r.Status)
	}
	if r.Config != h.config || r.PollInterval != "1h" || r.RescanInterval != "30m" || r.VerifyInterval != "5m" || r.NightMode.Enabled || r.NightMode.Phase != "off" {
		t.Errorf("configuration in the status = %+v", r.Status)
	}
	if !m.ColorControl || m.RGB == nil || *m.RGB != (ipc.RGB{R: 50, G: 50, B: 50}) || m.RGBTarget != nil {
		t.Errorf("colors in the status = %+v", m)
	}
	if len(m.Schedule) != 1 || m.Schedule[0].Time != "00:00" || m.Schedule[0].Brightness != 50 {
		t.Errorf("schedule in the status = %+v", m.Schedule)
	}
	if !h.send(ipc.CmdPause).OK || !h.send(ipc.CmdStatus).Paused {
		t.Error("pause did not take effect")
	}
	if !h.send(ipc.CmdResume).OK || h.send(ipc.CmdStatus).Paused {
		t.Error("resume did not take effect")
	}
	if n := len(h.ddc.SetCalls()); n != 0 {
		t.Errorf("no write was expected, got %d", n)
	}
}

func TestReloadKeepsOldConfigWhenInvalid(t *testing.T) {
	h := start(t)
	if err := os.WriteFile(h.config, []byte("[[monitor]]\nname = \"desk\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := h.send(ipc.CmdReload)
	if r.OK || !strings.Contains(r.Error, "invalid configuration") {
		t.Errorf("reload of an invalid file = %+v", r)
	}
	if got := h.send(ipc.CmdStatus).Monitors; len(got) != 1 || got[0].Name != "desk" {
		t.Errorf("the old config should still be active: %+v", got)
	}
}

func TestReloadAppliesNewConfig(t *testing.T) {
	h := start(t)
	renamed := strings.Replace(goodConfig, `name = "desk"`, `name = "renamed"`, 1)
	if err := os.WriteFile(h.config, []byte(renamed), 0o600); err != nil {
		t.Fatal(err)
	}
	if !h.send(ipc.CmdReload).OK {
		t.Fatal("reload failed")
	}
	h.waitFor(func(r ipc.Response) bool { return len(r.Monitors) == 1 && r.Monitors[0].Name == "renamed" }, "the new name")
}

func TestStopEndsTheDaemonAndRemovesTheSocket(t *testing.T) {
	h := start(t)
	if !h.send(ipc.CmdStop).OK {
		t.Fatal("stop was not acknowledged")
	}
	select {
	case err := <-h.done:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop")
	}
	if _, err := os.Stat(h.sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket should be gone: %v", err)
	}
}

func TestSecondInstanceIsRefused(t *testing.T) {
	h := start(t)
	err := daemon.Run(context.Background(), daemon.Params{
		ConfigPath: h.config, SocketPath: h.sock, StatePath: filepath.Join(filepath.Dir(h.sock), "s2.json"), DDC: h.ddc,
	})
	if !errors.Is(err, ipc.ErrAlreadyRunning) {
		t.Errorf("second Run = %v", err)
	}
}

func TestInvalidConfigAtStartup(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(bad, []byte("nonsense ["), 0o600); err != nil {
		t.Fatal(err)
	}
	err := daemon.Run(context.Background(), daemon.Params{ConfigPath: bad, SocketPath: filepath.Join(dir, "s"), StatePath: filepath.Join(dir, "st")})
	if err == nil {
		t.Error("expected an error")
	}
}
