// Filename: internal/tui/model_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"dusk/internal/ipc"
)

type fakeClient struct {
	mu    sync.Mutex
	st    *ipc.Status
	err   error
	doErr error
	cmds  []string
}

func (f *fakeClient) Status(context.Context) (*ipc.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, f.err
}

func (f *fakeClient) Do(_ context.Context, cmd string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	return f.doErr
}

func sampleStatus() *ipc.Status {
	return &ipc.Status{
		Night: 0.57, NightMode: ipc.NightStatus{Enabled: true, Start: "19:00", End: "07:00", Ramp: "30m", Phase: "fading in", NextChange: "19:29"},
		Warnings: []string{"another program keeps changing the colors of DP-1 (is GNOME Night Light on?)"},
		Monitors: []ipc.MonitorStatus{
			{Name: "Direita", Key: "24G4#S", Output: "DP-1", BrightnessBackend: "software", Current: 18, Target: 18, Gamma: ipc.RGB{R: 100, G: 89, B: 60}, Online: true},
			{Name: "Centro", Key: "LG", Output: "HDMI-1", BrightnessBackend: "ddc", Current: 20, Target: 22, Gamma: ipc.RGB{R: 100, G: 100, B: 100}, Online: false, LastError: "i2c hung"},
		},
	}
}

// run feeds msg to the model and then executes the returned command (one level), feeding its message back.
func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, cmd := m.Update(msg)
	m = next.(Model)
	if cmd == nil {
		return m
	}
	if out := cmd(); out != nil {
		switch out.(type) {
		case statusMsg, actionMsg, savedMsg, configMsg, screensMsg, svcStateMsg, svcResultMsg, logsMsg, resetPlanMsg, resetDoneMsg:
			next, _ = m.Update(out)
			m = next.(Model)
		}
	}
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func loaded(t *testing.T, c Client) Model {
	t.Helper()
	m := New(context.Background(), Deps{Client: c})
	return step(t, m, statusMsg{seq: 1, st: sampleStatusFrom(c), err: errFrom(c)})
}

func sampleStatusFrom(c Client) *ipc.Status { st, _ := c.Status(context.Background()); return st }
func errFrom(c Client) error                { _, err := c.Status(context.Background()); return err }

func TestStatusViewShowsEveryMonitor(t *testing.T) {
	m := loaded(t, &fakeClient{st: sampleStatus()})
	v := m.View()
	for _, want := range []string{
		"dusk", "running", "night 57% (fading in)", "next 19:29",
		"Monitor", "Direita", "DP-1", " 18%", "[=o--", "100 89 60", "3", " K", "software", "online",
		"Centro", "HDMI-1", "20% -> 22%", "ddc", "offline",
		"! Centro: i2c hung", "! another program keeps changing the colors of DP-1",
		"1 Status", "2 Monitors", "3 Night", "4 Service", "p pause/resume",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestViewFitsEightyByTwentyFour(t *testing.T) {
	m := loaded(t, &fakeClient{st: sampleStatus()})
	lines := strings.Split(m.View(), "\n")
	if len(lines) > minHeight {
		t.Errorf("view has %d lines", len(lines))
	}
	for i, l := range lines {
		if w := len([]rune(stripANSI(l))); w > minWidth {
			t.Errorf("line %d is %d columns wide: %q", i, w, l)
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestLongLinesAreTruncated(t *testing.T) {
	st := sampleStatus()
	st.Monitors[0].LastError = strings.Repeat("x", 300)
	m := loaded(t, &fakeClient{st: st})
	for _, l := range strings.Split(m.View(), "\n") {
		if w := len([]rune(stripANSI(l))); w > minWidth {
			t.Fatalf("line too wide (%d): %q", w, l)
		}
	}
}

func TestTooSmallTerminal(t *testing.T) {
	m := loaded(t, &fakeClient{st: sampleStatus()})
	m = step(t, m, tea.WindowSizeMsg{Width: 60, Height: 10})
	if v := m.View(); !strings.Contains(v, "terminal too small (need 80x24, have 60x10)") {
		t.Errorf("view = %q", v)
	}
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if strings.Contains(m.View(), "too small") {
		t.Error("a big terminal must render normally")
	}
}

func TestDaemonStopped(t *testing.T) {
	m := loaded(t, &fakeClient{err: ipc.ErrNoDaemon})
	v := m.View()
	for _, want := range []string{"stopped", "dusk is not running", "dusk start", "Service tab"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestConnectingBeforeTheFirstAnswer(t *testing.T) {
	m := New(context.Background(), Deps{Client: &fakeClient{}})
	if v := m.View(); !strings.Contains(v, "connecting") || !strings.Contains(v, "asking the daemon") {
		t.Errorf("view = %q", v)
	}
}

func TestRecoversWhenTheDaemonComesBack(t *testing.T) {
	c := &fakeClient{err: ipc.ErrNoDaemon}
	m := loaded(t, c)
	m = step(t, m, statusMsg{seq: m.seq + 1, st: sampleStatus()})
	if !strings.Contains(m.View(), "running") || !strings.Contains(m.View(), "Direita") {
		t.Errorf("view = %s", m.View())
	}
}

func TestLateAnswersAreDropped(t *testing.T) {
	m := New(context.Background(), Deps{Client: &fakeClient{}})
	m = step(t, m, statusMsg{seq: 5, st: sampleStatus()})
	m = step(t, m, statusMsg{seq: 3, err: ipc.ErrNoDaemon})
	if m.st == nil {
		t.Fatal("an older answer replaced a newer one")
	}
}

func TestTabsSwitchByKeys(t *testing.T) {
	m := loaded(t, &fakeClient{st: sampleStatus()})
	for _, c := range []struct {
		k    string
		want tab
	}{{"2", tabMonitors}, {"3", tabNight}, {"4", tabService}, {"1", tabStatus}} {
		if m = step(t, m, key(c.k)); m.tab != c.want {
			t.Errorf("key %s: tab = %d, want %d", c.k, m.tab, c.want)
		}
	}
	m = step(t, m, key("tab"))
	if m.tab != tabMonitors {
		t.Errorf("tab: %d", m.tab)
	}
	m = step(t, m, key("shift+tab"))
	m = step(t, m, key("shift+tab"))
	if m.tab != tabService {
		t.Errorf("shift+tab wraps to Service, got %d", m.tab)
	}
	if !strings.Contains(m.View(), "service control is not available") {
		t.Errorf("placeholder missing:\n%s", m.View())
	}
}

func TestPauseResumeReload(t *testing.T) {
	c := &fakeClient{st: sampleStatus()}
	m := loaded(t, c)
	m = step(t, m, key("p"))
	if got := c.cmds; len(got) != 1 || got[0] != ipc.CmdPause {
		t.Fatalf("cmds = %v", got)
	}
	if !strings.Contains(m.View(), "pause: ok") {
		t.Errorf("feedback missing:\n%s", m.View())
	}
	st := sampleStatus()
	st.Paused = true
	m = step(t, m, statusMsg{seq: m.seq + 1, st: st})
	if !strings.Contains(m.View(), "paused") {
		t.Errorf("view should say paused:\n%s", m.View())
	}
	m = step(t, m, key("p"))
	m = step(t, m, key("r"))
	if got := c.cmds; len(got) != 3 || got[1] != ipc.CmdResume || got[2] != ipc.CmdReload {
		t.Fatalf("cmds = %v", got)
	}
}

func TestActionFailuresAreShown(t *testing.T) {
	c := &fakeClient{st: sampleStatus(), doErr: errors.New("invalid configuration: x")}
	m := step(t, loaded(t, c), key("r"))
	if !strings.Contains(m.View(), "reload failed: invalid configuration: x") {
		t.Errorf("view:\n%s", m.View())
	}
	c.doErr = ipc.ErrNoDaemon
	m = step(t, m, key("p"))
	if !strings.Contains(m.View(), "dusk is not running") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestQuitKeys(t *testing.T) {
	m := loaded(t, &fakeClient{st: sampleStatus()})
	for _, k := range []string{"q", "ctrl+c"} {
		_, cmd := m.Update(key(k))
		if cmd == nil {
			t.Fatalf("%s: no command", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s did not quit", k)
		}
	}
}

func TestHelpToggles(t *testing.T) {
	m := loaded(t, &fakeClient{st: sampleStatus()})
	m = step(t, m, key("?"))
	if !strings.Contains(m.View(), "close help") {
		t.Errorf("help not shown:\n%s", m.View())
	}
	m = step(t, m, key("?"))
	if strings.Contains(m.View(), "close help") {
		t.Error("help should close")
	}
}

func TestNightOffHeader(t *testing.T) {
	st := sampleStatus()
	st.NightMode = ipc.NightStatus{}
	if v := loaded(t, &fakeClient{st: st}).View(); !strings.Contains(v, "night off") {
		t.Errorf("view:\n%s", v)
	}
}

func TestNoScreensYet(t *testing.T) {
	if v := loaded(t, &fakeClient{st: &ipc.Status{}}).View(); !strings.Contains(v, "no screens under a schedule yet") {
		t.Errorf("view:\n%s", v)
	}
}
