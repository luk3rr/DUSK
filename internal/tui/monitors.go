// Filename: internal/tui/monitors.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"

	"dusk/internal/config"
	"dusk/internal/gamma"
	"dusk/internal/ipc"
)

// ---- messages ----

type configMsg struct {
	cfg *config.Config
	err error
}

type screensMsg struct {
	outs []gamma.Output
	err  error
}

type savedMsg struct {
	cfg    *config.Config
	err    error
	notice string
}

// ---- state ----

type monitorsView struct {
	cursor   int
	ed       *form
	edIndex  int // index in cfg.Monitors, or -1 for a new monitor
	deleting bool
}

type nightView struct {
	ed *form
}

// editing reports whether a form owns the keyboard.
func (m Model) editing() bool { return m.mon.ed != nil || m.night.ed != nil || m.serviceActive() }

func (m Model) loadConfig() tea.Cmd {
	if m.store == nil {
		return nil
	}
	store := m.store
	return func() tea.Msg {
		cfg, err := store.Load()
		return configMsg{cfg: cfg, err: err}
	}
}

func (m Model) loadScreens() tea.Cmd {
	if m.screens == nil {
		return nil
	}
	src, ctx := m.screens, m.ctx
	return func() tea.Msg {
		outs, err := src.Screens(ctx)
		return screensMsg{outs: outs, err: err}
	}
}

// saveCmd writes cand, reads it back, and asks the daemon to reload.
func (m Model) saveCmd(cand *config.Config) tea.Cmd {
	store, c, ctx := m.store, m.client, m.ctx
	return func() tea.Msg {
		if err := store.Save(cand); err != nil {
			return savedMsg{err: err}
		}
		cfg, err := store.Load()
		if err != nil {
			cfg = cand
		}
		notice := "saved and reloaded"
		switch err := c.Do(ctx, ipc.CmdReload); {
		case errors.Is(err, ipc.ErrNoDaemon):
			notice = "saved; the daemon is not running"
		case err != nil:
			notice = "saved; reload failed: " + err.Error()
		}
		return savedMsg{cfg: cfg, notice: notice}
	}
}

// withMonitors returns a copy of the config whose monitor list is mons.
func (m Model) withMonitors(mons []config.Monitor) *config.Config {
	c := *m.cfg
	c.Monitors = mons
	return &c
}

func (m Model) copyMonitors() []config.Monitor {
	return append([]config.Monitor(nil), m.cfg.Monitors...)
}

// ---- updates ----

func (m Model) onConfig(msg configMsg) Model {
	m.cfgLoaded = true
	m.cfg, m.cfgErr = msg.cfg, msg.err
	if msg.err != nil {
		m.cfg = nil
	}
	return m
}

func (m Model) onSaved(msg savedMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		text := problems(msg.err)
		switch {
		case m.mon.ed != nil:
			m.mon.ed.errLine = text
		case m.night.ed != nil:
			m.night.ed.errLine = text
		default:
			m.notice = "save failed: " + text
		}
		return m, nil
	}
	m.cfg, m.cfgErr = msg.cfg, nil
	m.mon.ed, m.night.ed, m.mon.deleting = nil, nil, false
	if m.cfg != nil && m.mon.cursor >= len(m.cfg.Monitors) {
		m.mon.cursor = max(0, len(m.cfg.Monitors)-1)
	}
	m.notice = msg.notice
	m.seq++
	return m, m.fetch(m.seq)
}

// monitorsKey handles keys on the Monitors tab.
func (m Model) monitorsKey(k tea.KeyMsg) (Model, tea.Cmd) {
	if m.mon.ed != nil {
		return m.editorKey(k)
	}
	if m.cfg == nil {
		if k.String() == "n" && m.store != nil && os.IsNotExist(m.cfgErr) {
			return m, m.saveCmd(defaultConfig(m.screenList))
		}
		return m, nil
	}
	n := len(m.cfg.Monitors)
	if m.mon.deleting {
		switch k.String() {
		case "y":
			mons := m.copyMonitors()
			mons = append(mons[:m.mon.cursor], mons[m.mon.cursor+1:]...)
			m.mon.deleting = false
			return m, m.saveCmd(m.withMonitors(mons))
		default:
			m.mon.deleting = false
		}
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		if n > 0 {
			m.mon.cursor = (m.mon.cursor + n - 1) % n
		}
	case "down", "j":
		if n > 0 {
			m.mon.cursor = (m.mon.cursor + 1) % n
		}
	case "enter":
		if n > 0 {
			m.mon.ed, m.mon.edIndex = monitorForm(m.cfg, m.cfg.Monitors[m.mon.cursor], m.mon.cursor), m.mon.cursor
		}
	case "a":
		name, match := "", ""
		if s, ok := m.unconfiguredScreen(); ok {
			name, match = orDefault(s.Product, s.Name), s.Key()
		}
		m.mon.ed, m.mon.edIndex = monitorForm(m.cfg, newMonitor(name, match), -1), -1
	case "d":
		if n > 0 {
			m.mon.deleting = true
		}
	}
	return m, nil
}

// unconfiguredScreen returns the first detected screen that has no entry in the config.
func (m Model) unconfiguredScreen() (gamma.Output, bool) {
	for _, s := range m.screenList {
		found := false
		for _, mon := range m.cfg.Monitors {
			if strings.EqualFold(mon.Match, s.Key()) || strings.EqualFold(mon.Match, s.Product) {
				found = true
			}
		}
		if !found {
			return s, true
		}
	}
	return gamma.Output{}, false
}

// editorKey handles keys while a monitor form is open.
func (m Model) editorKey(k tea.KeyMsg) (Model, tea.Cmd) {
	f := m.mon.ed
	switch f.key(k) {
	case actCancel:
		m.mon.ed = nil
	case actAddRow:
		f.addPoint()
	case actDeleteRow:
		if f.fields[f.focus].group >= 0 {
			f.deletePoint()
		}
	case actSave:
		ok := f.validate()
		if f.duplicateTimes() {
			ok = false
		}
		if ok && !hasPoint(f) {
			f.errLine = "the schedule needs at least one point (Ctrl+A adds one)"
			ok = false
		}
		if !ok {
			return m, nil
		}
		mons := m.copyMonitors()
		mon := monitorFromForm(f)
		if m.mon.edIndex < 0 {
			mons = append(mons, mon)
		} else {
			mons[m.mon.edIndex] = mon
		}
		cand := m.withMonitors(mons)
		if err := config.Validate(cand); err != nil {
			f.errLine = problems(err)
			return m, nil
		}
		return m, m.saveCmd(cand)
	}
	return m, nil
}

func hasPoint(f *form) bool {
	for _, fl := range f.fields {
		if fl.group >= 0 {
			return true
		}
	}
	return false
}

// nightKey handles keys on the Night tab.
func (m Model) nightKey(k tea.KeyMsg) (Model, tea.Cmd) {
	if m.night.ed == nil {
		if k.String() == "enter" && m.cfg != nil {
			m.night.ed = nightForm(m.cfg.Night)
		}
		return m, nil
	}
	f := m.night.ed
	switch f.key(k) {
	case actCancel:
		m.night.ed = nil
	case actSave:
		if !f.validate() {
			return m, nil
		}
		cand := *m.cfg
		cand.Night = nightFromForm(f, m.cfg.Night)
		if err := config.Validate(&cand); err != nil {
			f.errLine = problems(err)
			return m, nil
		}
		return m, m.saveCmd(&cand)
	}
	return m, nil
}

// ---- views ----

func (m Model) configUnavailable() (string, bool) {
	switch {
	case m.store == nil:
		return dimStyle.Render("configuration editing is not available"), true
	case !m.cfgLoaded:
		return dimStyle.Render("reading the configuration..."), true
	case m.cfg == nil && os.IsNotExist(m.cfgErr):
		var b strings.Builder
		b.WriteString("No configuration yet (" + m.store.Path() + ").\n\n")
		if len(m.screenList) > 0 {
			b.WriteString("Detected screens:\n")
			for _, s := range m.screenList {
				fmt.Fprintf(&b, "  %-10s %s\n", s.Name, s.Key())
			}
			b.WriteString("\nPress n to create a configuration with one entry per screen.")
		} else {
			b.WriteString("No screens were detected")
			if m.screensErr != nil {
				b.WriteString(": " + m.screensErr.Error())
			}
			b.WriteString(".\nPress n to create an empty configuration; add monitors with a.")
		}
		return b.String(), true
	case m.cfg == nil:
		return errStyle.Render("cannot read the configuration: "+m.cfgErr.Error()) + "\n\nFix the file by hand or move it away; dusk never overwrites a file it cannot read.", true
	}
	return "", false
}

func (m Model) monitorsBody() string {
	if s, bad := m.configUnavailable(); bad {
		return s
	}
	if m.mon.ed != nil {
		return titleStyle.Render(m.mon.ed.title) + "\n" + m.mon.ed.render(m.width, m.height-9, true) +
			"\n" + dimStyle.Render("Up/Down field  Left/Right adjust  Shift+Left/Right big step  Ctrl+S save\nCtrl+A add schedule point  Ctrl+D delete point  Esc cancel")
	}
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  Name\tMatch\tBrightness\tPoints\tNight color")
	for i, mon := range m.cfg.Monitors {
		cur := "  "
		if i == m.mon.cursor {
			cur = "> "
		}
		nm := mon.NightFor(m.cfg.Night)
		color := fmt.Sprintf("R%d G%d B%d", nm.RedGain, nm.GreenGain, nm.BlueGain)
		if nm.Temperature > 0 {
			color = fmt.Sprintf("%d K", nm.Temperature)
		}
		fmt.Fprintf(w, "%s%s\t%s\t%s\t%d\t%s\n", cur, mon.Name, mon.Match, mon.Brightness, len(mon.Points), color)
	}
	w.Flush()
	out := strings.TrimRight(buf.String(), "\n")
	if len(m.cfg.Monitors) == 0 {
		out += "\n\nNo monitors yet."
	}
	if m.mon.deleting {
		return out + "\n\n" + warnStyle.Render(fmt.Sprintf("Delete %q? y = yes, any other key = no", m.cfg.Monitors[m.mon.cursor].Name))
	}
	if s, ok := m.unconfiguredScreen(); ok {
		out += "\n\n" + dimStyle.Render(fmt.Sprintf("Screen without an entry: %s (%s). Press a to add it.", s.Name, s.Key()))
	}
	return out + "\n\n" + dimStyle.Render("Up/Down select   Enter edit   a add   d delete")
}

func (m Model) nightBody() string {
	if s, bad := m.configUnavailable(); bad {
		return s
	}
	if m.night.ed != nil {
		return titleStyle.Render(m.night.ed.title) + "\n" + m.night.ed.render(m.width, m.height-9, true) +
			"\n" + dimStyle.Render("Up/Down field  Left/Right adjust  Shift+Left/Right big step  Home/End min/max\nCtrl+S save   Esc cancel")
	}
	n := m.cfg.Night
	var b strings.Builder
	if !n.Enabled {
		b.WriteString("Night mode is off.\n")
	} else {
		fmt.Fprintf(&b, "Night mode is on: %s -> %s, fade %s\n", config.FormatClock(n.Start), config.FormatClock(n.End), config.FormatDuration(orRamp(n.Ramp)))
	}
	if n.Temperature > 0 {
		fmt.Fprintf(&b, "Warmth at the peak: %d K  %s  R %d  G %d  B %d\n", n.Temperature, swatch(n.RedGain, n.GreenGain, n.BlueGain), n.RedGain, n.GreenGain, n.BlueGain)
		b.WriteString(bar(n.Temperature, config.MinKelvin, config.NeutralKelvin, sliderWidth) + "  " + dimStyle.Render(hintWarmth) + "\n")
	} else {
		fmt.Fprintf(&b, "Color at the peak (custom RGB): %s  R %d  G %d  B %d\n", swatch(n.RedGain, n.GreenGain, n.BlueGain), n.RedGain, n.GreenGain, n.BlueGain)
	}
	b.WriteString("\n" + dimStyle.Render("Enter edit"))
	return b.String()
}
