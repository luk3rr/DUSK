// Filename: internal/tui/service.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"dusk/internal/service"
)

const logLines = 200

// Service is what the Service tab needs from the system.
type Service interface {
	State(ctx context.Context) service.State
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	// Install and Uninstall return a text that says what was done.
	Install(ctx context.Context) (string, error)
	Uninstall(ctx context.Context) (string, error)
	// Logs returns the last n log lines.
	Logs(ctx context.Context, n int) ([]string, error)
}

// ResetItem is what restoring one monitor's DDC color gains would do.
type ResetItem struct {
	Key        string
	Current    [3]int
	Target     [3]int
	FromState  bool // Target is the original saved by an older dusk
	NoOriginal bool // nothing saved and the maximum was not requested: cannot be applied
	Normal     bool // already at the target
}

// DDCReset restores the RGB gains that older dusk versions left in the monitors.
type DDCReset interface {
	// Plan reads the monitors; it never writes.
	Plan(ctx context.Context, toMax bool) ([]ResetItem, error)
	// Apply writes the gains of one monitor.
	Apply(ctx context.Context, key string, toMax bool) error
}

type svcMode int

const (
	svcMenu svcMode = iota
	svcLogs
	svcConfirmUninstall
	svcReset
	svcConfirmRestore
)

type serviceView struct {
	mode      svcMode
	state     service.State
	known     bool
	ticks     int
	result    string
	logs      []string
	logOff    int
	items     []ResetItem
	cursor    int
	toMax     bool
	resetNote string
	resetOK   string
	working   bool
}

type svcStateMsg struct{ st service.State }

type svcResultMsg struct {
	what string
	out  string
	err  error
}

type logsMsg struct {
	lines []string
	err   error
}

type resetPlanMsg struct {
	items []ResetItem
	err   error
}

type resetDoneMsg struct {
	key string
	err error
}

func (m Model) svcState() tea.Cmd {
	if m.svc == nil {
		return nil
	}
	s, ctx := m.svc, m.ctx
	return func() tea.Msg { return svcStateMsg{st: s.State(ctx)} }
}

func (m Model) svcRun(what string, f func(ctx context.Context) (string, error)) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		out, err := f(ctx)
		return svcResultMsg{what: what, out: out, err: err}
	}
}

func (m Model) loadLogs() tea.Cmd {
	s, ctx := m.svc, m.ctx
	return func() tea.Msg {
		lines, err := s.Logs(ctx, logLines)
		return logsMsg{lines: lines, err: err}
	}
}

func (m Model) planReset() tea.Cmd {
	d, ctx, toMax := m.ddc, m.ctx, m.sv.toMax
	return func() tea.Msg {
		items, err := d.Plan(ctx, toMax)
		return resetPlanMsg{items: items, err: err}
	}
}

func (m Model) applyReset(key string) tea.Cmd {
	d, ctx, toMax := m.ddc, m.ctx, m.sv.toMax
	return func() tea.Msg { return resetDoneMsg{key: key, err: d.Apply(ctx, key, toMax)} }
}

// serviceActive reports whether the Service tab owns the keyboard (anything but its menu).
func (m Model) serviceActive() bool { return m.tab == tabService && m.sv.mode != svcMenu }

// serviceMsg handles the messages of the Service tab.
func (m Model) serviceMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case svcStateMsg:
		m.sv.state, m.sv.known = msg.st, true
	case svcResultMsg:
		m.sv.working = false
		if msg.err != nil {
			m.sv.result = fmt.Sprintf("%s failed: %v", msg.what, msg.err)
		} else {
			m.sv.result = strings.TrimSpace(msg.what + ": ok\n" + msg.out)
		}
		m.seq++
		return m, tea.Batch(m.svcState(), m.fetch(m.seq)), true
	case logsMsg:
		if msg.err != nil {
			m.sv.result = "cannot read the log: " + msg.err.Error()
			m.sv.mode = svcMenu
			return m, nil, true
		}
		m.sv.logs = msg.lines
		m.sv.logOff = max(0, len(msg.lines)-m.logHeight())
	case resetPlanMsg:
		if msg.err != nil {
			m.sv.resetNote = "some monitors could not be read: " + msg.err.Error()
		} else {
			m.sv.resetNote = ""
		}
		m.sv.items = msg.items
		m.sv.cursor = min(m.sv.cursor, max(0, len(msg.items)-1))
	case resetDoneMsg:
		m.sv.mode = svcReset
		if msg.err != nil {
			m.sv.resetNote = fmt.Sprintf("restoring %s failed: %v", msg.key, msg.err)
			return m, nil, true
		}
		m.sv.resetOK = "restored the DDC color gains of " + msg.key
		return m, m.planReset(), true
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m Model) logHeight() int { return max(3, m.height-9) }

// serviceKey handles keys on the Service tab.
func (m Model) serviceKey(k tea.KeyMsg) (Model, tea.Cmd) {
	if m.svc == nil {
		return m, nil
	}
	switch m.sv.mode {
	case svcLogs:
		return m.logsKey(k)
	case svcConfirmUninstall:
		m.sv.mode = svcMenu
		if k.String() == "y" {
			m.sv.working = true
			return m, m.svcRun("uninstall", m.svc.Uninstall)
		}
		return m, nil
	case svcReset:
		return m.resetKey(k)
	case svcConfirmRestore:
		m.sv.mode = svcReset
		if k.String() == "y" && m.sv.cursor < len(m.sv.items) {
			return m, m.applyReset(m.sv.items[m.sv.cursor].Key)
		}
		return m, nil
	}
	if m.sv.working {
		return m, nil
	}
	switch k.String() {
	case "i":
		m.sv.working = true
		return m, m.svcRun("install", m.svc.Install)
	case "u":
		m.sv.mode = svcConfirmUninstall
	case "s":
		m.sv.working = true
		return m, m.svcRun("start", func(ctx context.Context) (string, error) { return "", m.svc.Start(ctx) })
	case "x":
		m.sv.working = true
		return m, m.svcRun("stop", func(ctx context.Context) (string, error) { return "", m.svc.Stop(ctx) })
	case "l":
		m.sv.mode = svcLogs
		m.sv.logs = nil
		return m, m.loadLogs()
	case "d":
		if m.ddc == nil {
			m.sv.result = "DDC color reset is not available"
			return m, nil
		}
		m.sv.mode, m.sv.items, m.sv.cursor, m.sv.resetNote = svcReset, nil, 0, ""
		return m, m.planReset()
	}
	return m, nil
}

func (m Model) logsKey(k tea.KeyMsg) (Model, tea.Cmd) {
	h := m.logHeight()
	last := max(0, len(m.sv.logs)-h)
	switch k.String() {
	case "esc":
		m.sv.mode = svcMenu
	case "r":
		return m, m.loadLogs()
	case "up", "k":
		m.sv.logOff = max(0, m.sv.logOff-1)
	case "down", "j":
		m.sv.logOff = min(last, m.sv.logOff+1)
	case "pgup":
		m.sv.logOff = max(0, m.sv.logOff-h)
	case "pgdown":
		m.sv.logOff = min(last, m.sv.logOff+h)
	case "home":
		m.sv.logOff = 0
	case "end":
		m.sv.logOff = last
	}
	return m, nil
}

func (m Model) resetKey(k tea.KeyMsg) (Model, tea.Cmd) {
	n := len(m.sv.items)
	switch k.String() {
	case "esc":
		m.sv.mode, m.sv.resetOK = svcMenu, ""
	case "up", "k":
		if n > 0 {
			m.sv.cursor = (m.sv.cursor + n - 1) % n
		}
	case "down", "j":
		if n > 0 {
			m.sv.cursor = (m.sv.cursor + 1) % n
		}
	case "m":
		m.sv.toMax = !m.sv.toMax
		return m, m.planReset()
	case "r":
		return m, m.planReset()
	case "y":
		if n == 0 {
			return m, nil
		}
		it := m.sv.items[m.sv.cursor]
		m.sv.resetOK = ""
		switch {
		case it.NoOriginal:
			m.sv.resetNote = "no saved original: press m to allow the maximum (100%) first"
		case it.Normal:
			m.sv.resetNote = "this monitor is already at its target"
		default:
			m.sv.mode = svcConfirmRestore
		}
	}
	return m, nil
}

// ---- views ----

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (m Model) serviceBody() string {
	if m.svc == nil {
		return dimStyle.Render("service control is not available")
	}
	switch m.sv.mode {
	case svcLogs:
		return m.logsBody()
	case svcReset, svcConfirmRestore:
		return m.resetBody()
	}
	var b strings.Builder
	if m.sv.known {
		fmt.Fprintf(&b, "dusk.service   installed: %s   running: %s\n\n", yesNo(m.sv.state.Installed), yesNo(m.sv.state.Active))
	} else {
		b.WriteString(dimStyle.Render("asking systemd...") + "\n\n")
	}
	b.WriteString("i install   u uninstall   s start   x stop   l logs   d reset DDC colors\n")
	switch {
	case m.sv.mode == svcConfirmUninstall:
		b.WriteString("\n" + warnStyle.Render("Uninstall the service? y = yes, any other key = no") + "\n")
	case m.sv.working:
		b.WriteString("\n" + dimStyle.Render("working...") + "\n")
	}
	if m.sv.result != "" {
		b.WriteString("\n" + m.sv.result + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("Install writes ~/.config/systemd/user/dusk.service and runs: systemctl --user enable --now dusk.service"))
	return b.String()
}

func (m Model) logsBody() string {
	var b bytes.Buffer
	b.WriteString(titleStyle.Render(fmt.Sprintf("Log (last %d lines)", logLines)) + "\n")
	if len(m.sv.logs) == 0 {
		b.WriteString(dimStyle.Render("reading the log...") + "\n")
	}
	h := m.logHeight()
	end := min(len(m.sv.logs), m.sv.logOff+h)
	for _, l := range m.sv.logs[min(m.sv.logOff, end):end] {
		b.WriteString(l + "\n")
	}
	b.WriteString(dimStyle.Render("Up/Down PgUp/PgDn Home/End scroll   r reload   Esc back"))
	return b.String()
}

func (m Model) resetBody() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Reset DDC color gains") + "\n")
	b.WriteString("Older dusk versions left color gains (R G B) in the monitors. Restore them here.\n")
	b.WriteString(dimStyle.Render("saved = original kept by an older dusk   maximum = 100%   no original = untouched") + "\n\n")
	if len(m.sv.items) == 0 {
		b.WriteString(dimStyle.Render("no monitor with DDC color control found (or still reading)") + "\n")
	}
	for i, it := range m.sv.items {
		cur := "  "
		if i == m.sv.cursor {
			cur = "> "
		}
		note := "saved"
		switch {
		case it.NoOriginal:
			note = "no original"
		case it.Normal:
			note = "normal"
		case !it.FromState:
			note = "maximum"
		}
		fmt.Fprintf(&b, "%s%-20s %3d %3d %3d  ->  %3d %3d %3d   %s\n", cur, it.Key,
			it.Current[0], it.Current[1], it.Current[2], it.Target[0], it.Target[1], it.Target[2], note)
	}
	if m.sv.resetOK != "" {
		b.WriteString("\n" + okStyle.Render(m.sv.resetOK) + "\n")
	}
	if m.sv.resetNote != "" {
		b.WriteString("\n" + warnStyle.Render(m.sv.resetNote) + "\n")
	}
	if m.sv.mode == svcConfirmRestore && m.sv.cursor < len(m.sv.items) {
		it := m.sv.items[m.sv.cursor]
		b.WriteString("\n" + warnStyle.Render(fmt.Sprintf("Write %d/%d/%d to %s? y = write, other key = cancel",
			it.Target[0], it.Target[1], it.Target[2], it.Key)) + "\n")
	} else {
		b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("Up/Down select  y restore  m use 100%% when none saved (%s)  r reload  Esc back", map[bool]string{true: "on", false: "off"}[m.sv.toMax])))
	}
	return b.String()
}
