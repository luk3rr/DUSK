// Filename: internal/tui/service_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"dusk/internal/service"
)

type fakeService struct {
	mu        sync.Mutex
	st        service.State
	calls     []string
	failWith  error
	installed string
	logs      []string
}

func (f *fakeService) note(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeService) State(context.Context) service.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

func (f *fakeService) Start(context.Context) error { f.note("start"); return f.failWith }
func (f *fakeService) Stop(context.Context) error  { f.note("stop"); return f.failWith }
func (f *fakeService) Install(context.Context) (string, error) {
	f.note("install")
	return f.installed, f.failWith
}
func (f *fakeService) Uninstall(context.Context) (string, error) {
	f.note("uninstall")
	return "", f.failWith
}
func (f *fakeService) Logs(context.Context, int) ([]string, error) {
	f.note("logs")
	return f.logs, f.failWith
}

type fakeReset struct {
	mu      sync.Mutex
	items   []ResetItem
	applied []string
	toMax   []bool
	err     error
}

func (f *fakeReset) Plan(_ context.Context, toMax bool) ([]ResetItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toMax = append(f.toMax, toMax)
	out := append([]ResetItem(nil), f.items...)
	for i := range out {
		if out[i].NoOriginal && toMax {
			out[i].NoOriginal = false
			out[i].Target = [3]int{100, 100, 100}
		}
	}
	return out, nil
}

func (f *fakeReset) Apply(_ context.Context, key string, toMax bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.applied = append(f.applied, fmt.Sprintf("%s toMax=%v", key, toMax))
	return nil
}

func (f *fakeReset) writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.applied...)
}

func serviceModel(t *testing.T, svc *fakeService, rs *fakeReset) Model {
	t.Helper()
	m := New(context.Background(), Deps{Client: &fakeClient{st: sampleStatus()}, Service: svc, DDC: rs})
	m = step(t, m, statusMsg{seq: 1, st: sampleStatus()})
	m = step(t, m, key("4"))
	return m
}

func TestServiceTabShowsTheState(t *testing.T) {
	m := serviceModel(t, &fakeService{st: service.State{Installed: true, Active: true}}, nil)
	// opening the tab asked systemd; apply the answer
	m = step(t, m, svcStateMsg{st: service.State{Installed: true, Active: true}})
	for _, want := range []string{"dusk.service", "installed: yes", "running: yes", "i install", "u uninstall", "l logs", "d reset DDC colors"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("view lacks %q:\n%s", want, m.View())
		}
	}
}

func TestServiceActions(t *testing.T) {
	svc := &fakeService{installed: "wrote the unit"}
	m := serviceModel(t, svc, nil)
	m = step(t, m, key("i"))
	if len(svc.calls) != 1 || svc.calls[0] != "install" || !strings.Contains(m.View(), "install: ok") || !strings.Contains(m.View(), "wrote the unit") {
		t.Fatalf("calls %v\n%s", svc.calls, m.View())
	}
	m = step(t, m, key("s"))
	m = step(t, m, key("x"))
	if got := strings.Join(svc.calls, ","); got != "install,start,stop" {
		t.Fatalf("calls = %s", got)
	}
}

func TestServiceFailureIsShown(t *testing.T) {
	svc := &fakeService{failWith: errors.New("unit not found")}
	m := step(t, serviceModel(t, svc, nil), key("s"))
	if !strings.Contains(m.View(), "start failed: unit not found") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestUninstallAsksFirst(t *testing.T) {
	svc := &fakeService{}
	m := step(t, serviceModel(t, svc, nil), key("u"))
	if !strings.Contains(m.View(), "Uninstall the service?") {
		t.Fatalf("no question:\n%s", m.View())
	}
	m = step(t, m, key("n"))
	if len(svc.calls) != 0 {
		t.Fatalf("a refused uninstall ran: %v", svc.calls)
	}
	m = step(t, step(t, m, key("u")), key("y"))
	if len(svc.calls) != 1 || svc.calls[0] != "uninstall" {
		t.Fatalf("calls = %v", svc.calls)
	}
}

func manyLines(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("line %03d", i+1)
	}
	return out
}

func TestLogsScrollAndClose(t *testing.T) {
	svc := &fakeService{logs: manyLines(100)}
	m := step(t, serviceModel(t, svc, nil), key("l"))
	if !strings.Contains(m.View(), "line 100") || strings.Contains(m.View(), "line 001") {
		t.Fatalf("it should open at the end of the log:\n%s", m.View())
	}
	if len(strings.Split(m.View(), "\n")) > minHeight {
		t.Errorf("the log view overflows the screen")
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyHome})
	if !strings.Contains(m.View(), "line 001") || strings.Contains(m.View(), "line 100") {
		t.Errorf("Home should show the start:\n%s", m.View())
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if strings.Contains(m.View(), "line 001") {
		t.Errorf("PgDn should scroll:\n%s", m.View())
	}
	// Typing q or p inside the log must not quit or pause.
	m = step(t, m, key("q"))
	m = step(t, m, key("p"))
	if m.sv.mode != svcLogs {
		t.Fatal("the log view was left by a global key")
	}
	m = step(t, m, k(tea.KeyEsc))
	if m.sv.mode != svcMenu {
		t.Error("Esc should close the log")
	}
}

func TestLogErrorGoesBackToTheMenu(t *testing.T) {
	svc := &fakeService{failWith: errors.New("journalctl: not found")}
	m := step(t, serviceModel(t, svc, nil), key("l"))
	if m.sv.mode != svcMenu || !strings.Contains(m.View(), "cannot read the log: journalctl: not found") {
		t.Errorf("view:\n%s", m.View())
	}
}

func resetItems() []ResetItem {
	return []ResetItem{
		{Key: "24G4#S1", Current: [3]int{50, 50, 50}, Target: [3]int{90, 95, 100}, FromState: true},
		{Key: "LG ULTRAWIDE", Current: [3]int{50, 50, 50}, Target: [3]int{100, 100, 100}, NoOriginal: true},
		{Key: "LF24T450F#S3", Current: [3]int{100, 100, 100}, Target: [3]int{100, 100, 100}, Normal: true, FromState: true},
	}
}

func TestResetListsWithoutWriting(t *testing.T) {
	rs := &fakeReset{items: resetItems()}
	m := step(t, serviceModel(t, &fakeService{}, rs), key("d"))
	for _, want := range []string{"Reset DDC color gains", "24G4#S1", " 50  50  50  ->   90  95 100   saved", "no original", "normal"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("view lacks %q:\n%s", want, m.View())
		}
	}
	if len(rs.writes()) != 0 {
		t.Fatal("listing must not write")
	}
}

func TestResetNeedsAConfirmationForTheShownMonitor(t *testing.T) {
	rs := &fakeReset{items: resetItems()}
	m := step(t, serviceModel(t, &fakeService{}, rs), key("d"))
	m = step(t, m, key("y"))
	if !strings.Contains(m.View(), "Write 90/95/100 to 24G4#S1?") {
		t.Fatalf("no final question:\n%s", m.View())
	}
	m = step(t, m, key("n")) // anything but y cancels
	if len(rs.writes()) != 0 {
		t.Fatalf("a cancelled restore wrote: %v", rs.writes())
	}
	m = step(t, step(t, m, key("y")), key("y"))
	if got := rs.writes(); len(got) != 1 || got[0] != "24G4#S1 toMax=false" {
		t.Fatalf("writes = %v", got)
	}
	if !strings.Contains(m.View(), "restored the DDC color gains of 24G4#S1") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestResetNeverAssumesTheMaximumUnlessAsked(t *testing.T) {
	rs := &fakeReset{items: resetItems()}
	m := step(t, serviceModel(t, &fakeService{}, rs), key("d"))
	m = step(t, step(t, m, key("down")), key("y")) // LG has no saved original
	if !strings.Contains(m.View(), "press m to allow the maximum (100%) first") || len(rs.writes()) != 0 {
		t.Fatalf("view:\n%s", m.View())
	}
	m = step(t, m, key("m"))
	if !strings.Contains(m.View(), "maximum") || !strings.Contains(m.View(), "(on)") {
		t.Errorf("view:\n%s", m.View())
	}
	m = step(t, step(t, m, key("y")), key("y"))
	if got := rs.writes(); len(got) != 1 || got[0] != "LG ULTRAWIDE toMax=true" {
		t.Fatalf("writes = %v", got)
	}
}

func TestResetOfAnAlreadyNormalMonitorIsRefused(t *testing.T) {
	rs := &fakeReset{items: resetItems()}
	m := step(t, serviceModel(t, &fakeService{}, rs), key("d"))
	m = step(t, step(t, step(t, m, key("down")), key("down")), key("y"))
	if !strings.Contains(m.View(), "already at its target") || len(rs.writes()) != 0 {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestResetFailureIsShown(t *testing.T) {
	rs := &fakeReset{items: resetItems(), err: errors.New("i2c timeout")}
	m := step(t, serviceModel(t, &fakeService{}, rs), key("d"))
	m = step(t, step(t, m, key("y")), key("y"))
	if !strings.Contains(m.View(), "restoring 24G4#S1 failed: i2c timeout") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestResetEscGoesBackAndGlobalKeysAreCaptured(t *testing.T) {
	rs := &fakeReset{items: resetItems()}
	c := &fakeClient{st: sampleStatus()}
	m := New(context.Background(), Deps{Client: c, Service: &fakeService{}, DDC: rs})
	m = step(t, m, statusMsg{seq: 1, st: sampleStatus()})
	m = step(t, step(t, m, key("4")), key("d"))
	m = step(t, m, key("p")) // must not pause while the reset list is open
	if len(c.cmds) != 0 {
		t.Fatalf("a global key leaked into the reset view: %v", c.cmds)
	}
	m = step(t, m, k(tea.KeyEsc))
	if m.sv.mode != svcMenu {
		t.Error("Esc should leave the reset view")
	}
}

func TestServiceStateRefreshesWhileTheTabIsOpen(t *testing.T) {
	svc := &fakeService{}
	m := serviceModel(t, svc, nil)
	for i := 0; i < 4; i++ {
		next, _ := m.Update(tickMsg{})
		m = next.(Model)
	}
	_, cmd := m.Update(tickMsg{}) // the fifth tick
	if cmd == nil {
		t.Fatal("no command")
	}
	if m.sv.ticks != 4 {
		t.Fatalf("ticks = %d", m.sv.ticks)
	}
}

func TestServiceTabWithoutAService(t *testing.T) {
	m := New(context.Background(), Deps{Client: &fakeClient{st: sampleStatus()}})
	m = step(t, m, key("4"))
	if !strings.Contains(m.View(), "service control is not available") {
		t.Errorf("view:\n%s", m.View())
	}
}
