// Filename: internal/tui/monitors_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"dusk/internal/config"
	"dusk/internal/gamma"
	"dusk/internal/ipc"
)

const baseConfig = `
[night]
enabled = true
start = "19:00"
end = "07:00"
ramp = "30m"
blue_gain = 30

[[monitor]]
name = "Right"
match = "24G4#S1"
points = [{ time = "08:00", brightness = 30 }, { time = "22:00", brightness = 15 }]

[[monitor]]
name = "Center"
match = "LG ULTRAWIDE"
points = [{ time = "08:00", brightness = 40 }]
`

type fakeStore struct {
	mu      sync.Mutex
	cfg     *config.Config
	loadErr error
	saveErr error
	saved   []*config.Config
}

func (f *fakeStore) Path() string { return "/home/x/.config/dusk/config.toml" }

func (f *fakeStore) Load() (*config.Config, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return f.cfg, nil
}

func (f *fakeStore) Save(cfg *config.Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	f.cfg = cfg
	f.saved = append(f.saved, cfg)
	return nil
}

func (f *fakeStore) last(t *testing.T) *config.Config {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.saved) == 0 {
		t.Fatal("nothing was saved")
	}
	return f.saved[len(f.saved)-1]
}

func (f *fakeStore) saves() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

type fakeScreens struct {
	outs []gamma.Output
	err  error
}

func (f fakeScreens) Screens(context.Context) ([]gamma.Output, error) { return f.outs, f.err }

func parsed(t *testing.T, src string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func screens() []gamma.Output {
	return []gamma.Output{
		{CRTC: 0, Name: "DP-1", Product: "24G4", Serial: "S1"},
		{CRTC: 1, Name: "HDMI-1", Product: "LG ULTRAWIDE"},
		{CRTC: 2, Name: "DP-3", Product: "LF24T450F", Serial: "S3"},
	}
}

// open builds a model on the Monitors tab with the given store.
func open(t *testing.T, store *fakeStore, c *fakeClient) Model {
	t.Helper()
	m := New(context.Background(), Deps{Client: c, Config: store, Screens: fakeScreens{outs: screens()}})
	m = step(t, m, statusMsg{seq: 1, st: sampleStatus()})
	cfg, err := store.Load()
	m = step(t, m, configMsg{cfg: cfg, err: err})
	m = step(t, m, screensMsg{outs: screens()})
	return step(t, m, key("2"))
}

func newStore(t *testing.T) *fakeStore { return &fakeStore{cfg: parsed(t, baseConfig)} }

func k(typ tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: typ} }

func typed(t *testing.T, m Model, s string) Model {
	t.Helper()
	return step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func backspaces(t *testing.T, m Model, n int) Model {
	t.Helper()
	for i := 0; i < n; i++ {
		m = step(t, m, k(tea.KeyBackspace))
	}
	return m
}

// replace clears the focused field and types s.
func replace(t *testing.T, m Model, s string) Model {
	t.Helper()
	return typed(t, backspaces(t, m, 40), s)
}

func down(t *testing.T, m Model, n int) Model {
	t.Helper()
	for i := 0; i < n; i++ {
		m = step(t, m, k(tea.KeyDown))
	}
	return m
}

func TestMonitorsListShowsTheConfiguration(t *testing.T) {
	m := open(t, newStore(t), &fakeClient{st: sampleStatus()})
	v := m.View()
	for _, want := range []string{"Name", "Match", "> Right", "24G4#S1", "software", "Center", "LG ULTRAWIDE", "Enter edit", "Screen without an entry: DP-3"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestEditAMonitorAndSave(t *testing.T) {
	store, c := newStore(t), &fakeClient{st: sampleStatus()}
	m := open(t, store, c)
	m = step(t, m, k(tea.KeyEnter))
	if m.mon.ed == nil || !strings.Contains(m.View(), "Edit monitor") || !strings.Contains(m.View(), "editing: Ctrl+S save") {
		t.Fatalf("editor not open:\n%s", m.View())
	}
	m = replace(t, m, "Desk")                       // Name
	m = down(t, m, 2)                               // Brightness
	m = step(t, m, k(tea.KeyRight))                 // software -> ddc
	m = step(t, down(t, m, 1), k(tea.KeyBackspace)) // Floor: "10" -> "1"
	m = typed(t, m, "5")                            // 15
	m = down(t, m, 3)                               // Night blue
	m = replace(t, m, "25")
	m = step(t, m, k(tea.KeyCtrlS))
	if store.saves() != 1 {
		t.Fatalf("saves = %d\n%s", store.saves(), m.View())
	}
	mon := store.last(t).Monitors[0]
	if mon.Name != "Desk" || mon.Brightness != "ddc" || mon.Floor != 15 || mon.Night.BlueGain == nil || *mon.Night.BlueGain != 25 {
		t.Fatalf("saved monitor = %+v", mon)
	}
	if len(store.last(t).Monitors) != 2 || store.last(t).Monitors[1].Name != "Center" {
		t.Errorf("the other monitor must be untouched: %+v", store.last(t).Monitors)
	}
	if got := c.cmds; len(got) != 1 || got[0] != ipc.CmdReload {
		t.Errorf("the daemon was not asked to reload: %v", got)
	}
	if m.mon.ed != nil || !strings.Contains(m.View(), "saved and reloaded") || !strings.Contains(m.View(), "Desk") {
		t.Errorf("view after saving:\n%s", m.View())
	}
}

func TestInvalidFieldsShowTheirMessageAndSaveNothing(t *testing.T) {
	store := newStore(t)
	m := open(t, store, &fakeClient{st: sampleStatus()})
	m = step(t, m, k(tea.KeyEnter))
	m = down(t, m, 3) // Floor
	m = replace(t, m, "80")
	m = down(t, m, 1) // Night red
	m = replace(t, m, "abc")
	m = step(t, m, k(tea.KeyCtrlS))
	v := m.View()
	for _, want := range []string{"a whole number from 0 to 50", "a whole number from 0 to 100"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if store.saves() != 0 || m.mon.ed == nil {
		t.Fatal("an invalid form must not be saved or closed")
	}
	if m.mon.ed.focus != 3 {
		t.Errorf("the focus should go to the first bad field, got %d", m.mon.ed.focus)
	}
}

func TestNameAndMatchMustBeUnique(t *testing.T) {
	store := newStore(t)
	m := step(t, open(t, store, &fakeClient{st: sampleStatus()}), k(tea.KeyEnter))
	m = replace(t, m, "center") // clashes with "Center", case-insensitively
	m = down(t, m, 1)
	m = replace(t, m, "LG ULTRAWIDE")
	m = step(t, m, k(tea.KeyCtrlS))
	if got := strings.Count(m.View(), "another monitor already uses this"); got != 2 {
		t.Errorf("want 2 uniqueness messages, got %d:\n%s", got, m.View())
	}
	if store.saves() != 0 {
		t.Fatal("saved a duplicate")
	}
}

func TestSchedulePointsCanBeAddedEditedAndRemoved(t *testing.T) {
	store := newStore(t)
	m := step(t, open(t, store, &fakeClient{st: sampleStatus()}), k(tea.KeyEnter))
	m = step(t, m, k(tea.KeyCtrlA)) // a third point, focused on its time
	m = replace(t, m, "06:30")
	m = down(t, m, 1)
	m = replace(t, m, "55")
	m = step(t, m, k(tea.KeyCtrlS))
	pts := store.last(t).Monitors[0].Points
	if len(pts) != 3 || pts[0].Minute != 6*60+30 || pts[0].Brightness != 55 || pts[2].Minute != 22*60 {
		t.Fatalf("points = %+v", pts)
	}
	// Remove the first point (the 06:30 one is now sorted first in the config).
	m = step(t, m, k(tea.KeyEnter))
	m = down(t, m, 7) // first point time
	m = step(t, m, k(tea.KeyCtrlD))
	m = step(t, m, k(tea.KeyCtrlS))
	if got := store.last(t).Monitors[0].Points; len(got) != 2 {
		t.Fatalf("points after delete = %+v", got)
	}
}

func TestTheScheduleNeedsAtLeastOnePointAndNoRepeatedTimes(t *testing.T) {
	store := newStore(t)
	m := step(t, open(t, store, &fakeClient{st: sampleStatus()}), k(tea.KeyDown)) // select Center (1 point)
	m = step(t, m, k(tea.KeyEnter))
	m = down(t, m, 7)
	m = step(t, m, k(tea.KeyCtrlD))
	m = step(t, m, k(tea.KeyCtrlS))
	if !strings.Contains(m.View(), "needs at least one point") || store.saves() != 0 {
		t.Errorf("view:\n%s", m.View())
	}
	m = step(t, m, k(tea.KeyCtrlA))
	m = step(t, m, k(tea.KeyCtrlA))
	m = replace(t, m, "12:00")
	m = down(t, m, 2)
	m = replace(t, m, "12:00")
	m = step(t, m, k(tea.KeyCtrlS))
	if !strings.Contains(m.View(), "already in the schedule") || store.saves() != 0 {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestEscCancelsWithoutSaving(t *testing.T) {
	store := newStore(t)
	m := step(t, open(t, store, &fakeClient{st: sampleStatus()}), k(tea.KeyEnter))
	m = replace(t, m, "Changed")
	m = step(t, m, k(tea.KeyEsc))
	if m.mon.ed != nil || store.saves() != 0 || strings.Contains(m.View(), "Changed") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestTypingInAFormDoesNotTriggerGlobalKeys(t *testing.T) {
	c := &fakeClient{st: sampleStatus()}
	m := step(t, open(t, newStore(t), c), k(tea.KeyEnter))
	m = typed(t, m, "pqr2")
	if m.tab != tabMonitors || len(c.cmds) != 0 || !strings.Contains(m.View(), "Rightpqr2") {
		t.Errorf("tab %d cmds %v\n%s", m.tab, c.cmds, m.View())
	}
}

func TestAddAMonitorPrefilledFromAnUnconfiguredScreen(t *testing.T) {
	store := newStore(t)
	m := step(t, open(t, store, &fakeClient{st: sampleStatus()}), key("a"))
	if m.mon.ed == nil || !strings.Contains(m.View(), "New monitor") || !strings.Contains(m.View(), "LF24T450F#S3") {
		t.Fatalf("view:\n%s", m.View())
	}
	m = step(t, m, k(tea.KeyCtrlS))
	got := store.last(t).Monitors
	if len(got) != 3 || got[2].Name != "LF24T450F" || got[2].Match != "LF24T450F#S3" || len(got[2].Points) != 3 {
		t.Fatalf("monitors = %+v", got)
	}
}

func TestDeleteAMonitor(t *testing.T) {
	store := newStore(t)
	m := open(t, store, &fakeClient{st: sampleStatus()})
	m = step(t, m, key("d"))
	if !strings.Contains(m.View(), `Delete "Right"?`) {
		t.Fatalf("no confirmation:\n%s", m.View())
	}
	m = step(t, m, key("x")) // anything but y cancels
	if store.saves() != 0 || strings.Contains(m.View(), "Delete") {
		t.Fatal("a cancelled delete changed something")
	}
	m = step(t, step(t, m, key("d")), key("y"))
	if got := store.last(t).Monitors; len(got) != 1 || got[0].Name != "Center" {
		t.Fatalf("monitors = %+v", got)
	}
	if m.mon.cursor != 0 {
		t.Errorf("cursor = %d", m.mon.cursor)
	}
}

func TestFirstRunCreatesAConfigurationFromTheScreens(t *testing.T) {
	store := &fakeStore{loadErr: os.ErrNotExist}
	m := open(t, store, &fakeClient{err: ipc.ErrNoDaemon, doErr: ipc.ErrNoDaemon})
	v := m.View()
	for _, want := range []string{"No configuration yet", "DP-1", "HDMI-1", "Press n to create"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	m = step(t, m, key("n"))
	cfg := store.last(t)
	if len(cfg.Monitors) != 3 || cfg.Monitors[0].Match != "24G4#S1" || cfg.Monitors[1].Match != "LG ULTRAWIDE" || !cfg.Night.Enabled {
		t.Fatalf("cfg = %+v", cfg)
	}
	if !strings.Contains(m.View(), "saved; the daemon is not running") || !strings.Contains(m.View(), "24G4#S1") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestAnUnreadableConfigIsNeverOverwritten(t *testing.T) {
	store := &fakeStore{loadErr: errors.New("invalid TOML: line 3")}
	m := open(t, store, &fakeClient{st: sampleStatus()})
	m = step(t, m, key("n"))
	if store.saves() != 0 || !strings.Contains(m.View(), "cannot read the configuration: invalid TOML: line 3") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestSaveFailureStaysInTheForm(t *testing.T) {
	store := newStore(t)
	store.saveErr = errors.New("permission denied")
	m := step(t, open(t, store, &fakeClient{st: sampleStatus()}), k(tea.KeyEnter))
	m = replace(t, m, "New name")
	m = step(t, m, k(tea.KeyCtrlS))
	if m.mon.ed == nil || !strings.Contains(m.View(), "permission denied") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestReloadRefusedByTheDaemonIsReported(t *testing.T) {
	c := &fakeClient{st: sampleStatus(), doErr: errors.New("invalid configuration: x")}
	m := step(t, open(t, newStore(t), c), k(tea.KeyEnter))
	m = replace(t, m, "Other")
	m = step(t, m, k(tea.KeyCtrlS))
	if !strings.Contains(m.View(), "saved; reload failed: invalid configuration: x") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestLongSchedulesStillFitTheScreen(t *testing.T) {
	store := newStore(t)
	m := step(t, open(t, store, &fakeClient{st: sampleStatus()}), k(tea.KeyEnter))
	for i := 0; i < 8; i++ {
		m = step(t, m, k(tea.KeyCtrlA))
	}
	lines := strings.Split(m.View(), "\n")
	if len(lines) > minHeight {
		t.Errorf("view has %d lines:\n%s", len(lines), m.View())
	}
	if !strings.Contains(m.View(), "Point 10") {
		t.Errorf("the focused (last) row must be visible:\n%s", m.View())
	}
}

// ---- Night tab ----

func nightModel(t *testing.T, store *fakeStore, c *fakeClient) Model {
	return step(t, open(t, store, c), key("3"))
}

func TestNightTabShowsTheCurrentSettings(t *testing.T) {
	m := nightModel(t, newStore(t), &fakeClient{st: sampleStatus()})
	for _, want := range []string{"Night mode is on: 19:00 -> 07:00, fade 30m", "R 100  G 100  B 30", "Enter edit"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("view lacks %q:\n%s", want, m.View())
		}
	}
}

func TestEditTheNightWindow(t *testing.T) {
	store, c := newStore(t), &fakeClient{st: sampleStatus()}
	m := step(t, nightModel(t, store, c), k(tea.KeyEnter))
	m = down(t, m, 1) // Start
	m = replace(t, m, "20:30")
	m = down(t, m, 2) // Fade ramp
	m = replace(t, m, "45m")
	m = down(t, m, 3) // Blue
	m = replace(t, m, "40")
	m = step(t, m, k(tea.KeyCtrlS))
	n := store.last(t).Night
	if !n.Enabled || n.Start != 20*60+30 || n.Ramp.Minutes() != 45 || n.BlueGain != 40 {
		t.Fatalf("night = %+v", n)
	}
	if m.night.ed != nil || !strings.Contains(m.View(), "20:30 -> 07:00, fade 45m") {
		t.Errorf("view:\n%s", m.View())
	}
	if len(c.cmds) != 1 || c.cmds[0] != ipc.CmdReload {
		t.Errorf("cmds = %v", c.cmds)
	}
}

func TestNightFieldErrorsAndCrossFieldErrors(t *testing.T) {
	store := newStore(t)
	m := step(t, nightModel(t, store, &fakeClient{st: sampleStatus()}), k(tea.KeyEnter))
	m = down(t, m, 1)
	m = replace(t, m, "25:99")
	m = down(t, m, 2)
	m = replace(t, m, "soon")
	m = step(t, m, k(tea.KeyCtrlS))
	for _, want := range []string{"use HH:MM, for example 21:30", "a duration such as 30m or 1h"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("view lacks %q:\n%s", want, m.View())
		}
	}
	// Fix the fields but make start equal to end: only the whole-config check can see it.
	m = replace(t, m, "07:00") // the focus is already on the first bad field (Start)
	m = down(t, m, 2)
	m = replace(t, m, "30m")
	m = step(t, m, k(tea.KeyCtrlS))
	if !strings.Contains(m.View(), "start and end must differ") || store.saves() != 0 {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestTurningNightModeOffDoesNotRequireTheTimes(t *testing.T) {
	store := newStore(t)
	m := step(t, nightModel(t, store, &fakeClient{st: sampleStatus()}), k(tea.KeyEnter))
	m = step(t, m, k(tea.KeyRight)) // on -> off
	m = down(t, m, 1)
	m = replace(t, m, "") // an empty start is fine while it is off
	m = step(t, m, k(tea.KeyCtrlS))
	if store.saves() != 1 || store.last(t).Night.Enabled {
		t.Fatalf("saves %d\n%s", store.saves(), m.View())
	}
	if !strings.Contains(m.View(), "Night mode is off") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestTabsWithoutAStoreSayEditingIsUnavailable(t *testing.T) {
	m := New(context.Background(), Deps{Client: &fakeClient{st: sampleStatus()}})
	m = step(t, m, key("2"))
	if !strings.Contains(m.View(), "configuration editing is not available") {
		t.Errorf("view:\n%s", m.View())
	}
}
