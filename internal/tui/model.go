// Filename: internal/tui/model.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"dusk/internal/config"
	"dusk/internal/gamma"
	"dusk/internal/ipc"
)

const (
	minWidth     = 80
	minHeight    = 24
	pollInterval = time.Second
)

type tab int

const (
	tabStatus tab = iota
	tabMonitors
	tabNight
	tabService
	numTabs
)

var tabNames = [numTabs]string{"Status", "Monitors", "Night", "Service"}

type tickMsg struct{}

type statusMsg struct {
	seq int
	st  *ipc.Status
	err error
}

type actionMsg struct {
	name string
	err  error
}

// Deps are the things the interface talks to. Config and Screens may be nil
// (the Monitors and Night tabs then say that editing is unavailable).
type Deps struct {
	Client  Client
	Config  ConfigStore
	Screens ScreenSource
	Service Service  // nil: the Service tab says it is unavailable
	DDC     DDCReset // nil: no DDC reset
}

// Model is the Bubble Tea model of the whole interface.
type Model struct {
	client  Client
	store   ConfigStore
	screens ScreenSource
	svc     Service
	ddc     DDCReset
	sv      serviceView
	ctx     context.Context

	cfg        *config.Config
	cfgErr     error
	cfgLoaded  bool
	screenList []gamma.Output
	screensErr error
	mon        monitorsView
	night      nightView

	tab           tab
	width, height int
	help          bool

	seq     int // id of the latest status request
	applied int // id of the latest status request whose answer was used
	st      *ipc.Status
	err     error // why the daemon cannot be reached (nil while it answers)
	loaded  bool  // at least one answer (or failure) has arrived
	notice  string
}

// New creates the model. ctx bounds the calls made to the daemon.
func New(ctx context.Context, d Deps) Model {
	return Model{client: d.Client, store: d.Config, screens: d.Screens, svc: d.Service, ddc: d.DDC, ctx: ctx, width: minWidth, height: minHeight}
}

// Run starts the interface in the terminal and returns when the user quits.
func Run(ctx context.Context, d Deps) error {
	_, err := tea.NewProgram(New(ctx, d), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if errors.Is(err, context.Canceled) || errors.Is(err, tea.ErrProgramKilled) {
		return nil
	}
	return err
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	m.seq++
	return tea.Batch(m.fetch(m.seq), tick(), m.loadConfig(), m.loadScreens())
}

func tick() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) fetch(seq int) tea.Cmd {
	c, ctx := m.client, m.ctx
	return func() tea.Msg {
		st, err := c.Status(ctx)
		return statusMsg{seq: seq, st: st, err: err}
	}
}

func (m Model) action(name, cmd string) tea.Cmd {
	c, ctx := m.client, m.ctx
	return func() tea.Msg { return actionMsg{name: name, err: c.Do(ctx, cmd)} }
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		m.seq++
		cmds := []tea.Cmd{m.fetch(m.seq), tick()}
		m.sv.ticks++
		if m.tab == tabService && m.sv.mode == svcMenu && m.sv.ticks%5 == 0 {
			cmds = append(cmds, m.svcState())
		}
		return m, tea.Batch(cmds...)
	case statusMsg:
		if msg.seq < m.applied {
			return m, nil // an older answer arrived late
		}
		m.applied, m.loaded = msg.seq, true
		m.st, m.err = msg.st, msg.err
		if msg.err != nil {
			m.st = nil
		}
	case svcStateMsg, svcResultMsg, logsMsg, resetPlanMsg, resetDoneMsg:
		next, cmd, _ := m.serviceMsg(msg)
		return next, cmd
	case configMsg:
		return m.onConfig(msg), nil
	case screensMsg:
		m.screenList, m.screensErr = msg.outs, msg.err
	case savedMsg:
		return m.onSaved(msg)
	case actionMsg:
		switch {
		case errors.Is(msg.err, ipc.ErrNoDaemon):
			m.notice = "dusk is not running (start it with: dusk start)"
		case msg.err != nil:
			m.notice = fmt.Sprintf("%s failed: %v", msg.name, msg.err)
		default:
			m.notice = msg.name + ": ok"
		}
		m.seq++
		return m, m.fetch(m.seq)
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.editing() {
		return m.tabKey(k)
	}
	switch k.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "?":
		m.help = !m.help
	case "tab":
		m.tab = (m.tab + 1) % numTabs
		return m, m.onTab()
	case "shift+tab":
		m.tab = (m.tab + numTabs - 1) % numTabs
		return m, m.onTab()
	case "1", "2", "3", "4":
		m.tab = tab(k.String()[0] - '1')
		return m, m.onTab()
	case "p":
		if m.st != nil && m.st.Paused {
			return m, m.action("resume", ipc.CmdResume)
		}
		return m, m.action("pause", ipc.CmdPause)
	case "r":
		return m, m.action("reload", ipc.CmdReload)
	default:
		return m.tabKey(k)
	}
	return m, nil
}

// onTab runs when a tab is opened: the Service tab refreshes the service state.
func (m Model) onTab() tea.Cmd {
	if m.tab == tabService {
		return m.svcState()
	}
	return nil
}

// tabKey sends a key to the tab that is showing.
func (m Model) tabKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.tab {
	case tabMonitors:
		return m.monitorsKey(k)
	case tabNight:
		return m.nightKey(k)
	case tabService:
		return m.serviceKey(k)
	}
	return m, nil
}

// ---- view ----

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	activeStyle = lipgloss.NewStyle().Bold(true).Reverse(true)
)

// View implements tea.Model.
func (m Model) View() string {
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("terminal too small (need %dx%d, have %dx%d)", minWidth, minHeight, m.width, m.height)
	}
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(m.tabBar() + "\n\n")
	var body string
	switch m.tab {
	case tabStatus:
		body = m.statusBody()
	case tabMonitors:
		body = m.monitorsBody()
	case tabNight:
		body = m.nightBody()
	default:
		body = m.serviceBody()
	}
	b.WriteString(body)
	lines := strings.Count(b.String(), "\n") + 1
	for ; lines < m.height-2; lines++ {
		b.WriteString("\n")
	}
	b.WriteString("\n" + m.footer())
	return m.fit(b.String())
}

// fit truncates every line to the terminal width.
func (m Model) fit(s string) string {
	style := lipgloss.NewStyle().MaxWidth(m.width)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = style.Render(l)
	}
	return strings.Join(lines, "\n")
}

func (m Model) header() string {
	state := dimStyle.Render("connecting...")
	switch {
	case !m.loaded:
	case m.st == nil:
		state = errStyle.Render("(x) stopped")
	case m.st.Paused:
		state = warnStyle.Render("(=) paused")
	default:
		state = okStyle.Render("(*) running")
	}
	h := titleStyle.Render("dusk") + "  " + state
	if m.st != nil {
		n := m.st.NightMode
		if n.Enabled {
			h += fmt.Sprintf("   night %.0f%% (%s)", m.st.Night*100, n.Phase)
			if n.NextChange != "" {
				h += "   next " + n.NextChange
			}
		} else {
			h += "   night off"
		}
	}
	return h
}

func (m Model) tabBar() string {
	parts := make([]string, numTabs)
	for i := range parts {
		label := fmt.Sprintf(" %d %s ", i+1, tabNames[i])
		if tab(i) == m.tab {
			parts[i] = activeStyle.Render(label)
		} else {
			parts[i] = label
		}
	}
	return strings.Join(parts, " ")
}

func (m Model) footer() string {
	keys := "1-4 tabs   p pause/resume   r reload   ? help   q quit"
	if m.editing() {
		keys = "editing: Ctrl+S save   Esc cancel   Ctrl+C quit"
	}
	if m.help {
		keys = "Tab or 1-4 tabs   p pause/resume   r reload config   q quit   ? close help"
	}
	if m.notice != "" {
		return m.notice + "\n" + dimStyle.Render(keys)
	}
	return "\n" + dimStyle.Render(keys)
}
