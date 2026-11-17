// Filename: internal/tui/status.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"bytes"
	"fmt"
	"strings"
	"text/tabwriter"

	"dusk/internal/ipc"
)

func (m Model) statusBody() string {
	if !m.loaded {
		return dimStyle.Render("asking the daemon...")
	}
	if m.st == nil {
		var b strings.Builder
		b.WriteString(errStyle.Render("dusk is not running") + "\n\n")
		b.WriteString("Start it from the Service tab (4) or with: dusk start\n")
		if m.err != nil && m.err.Error() != "" {
			b.WriteString("\n" + dimStyle.Render(m.err.Error()))
		}
		return b.String()
	}
	if len(m.st.Monitors) == 0 {
		return "no screens under a schedule yet\n" + dimStyle.Render("add one in the Monitors tab (2)")
	}
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "Monitor\tScreen\tBrightness\tColor R G B\tBackend\tState")
	for _, mon := range m.st.Monitors {
		level := fmt.Sprintf("%s %3d%%", bar(mon.Current, 0, 100, 12), mon.Current)
		if mon.Current != mon.Target {
			level += fmt.Sprintf(" -> %d%%", mon.Target)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d %d %d\t%s\t%s\n",
			mon.Name, mon.Output, level, mon.Gamma.R, mon.Gamma.G, mon.Gamma.B,
			backendLabel(mon), stateLabel(mon))
	}
	w.Flush()
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	lines[0] = titleStyle.Render(lines[0])
	out := strings.Join(lines, "\n")
	for _, mon := range m.st.Monitors {
		if mon.LastError != "" {
			out += "\n" + errStyle.Render(fmt.Sprintf("! %s: %s", mon.Name, mon.LastError))
		}
	}
	for _, w := range m.st.Warnings {
		out += "\n" + warnStyle.Render("! "+w)
	}
	return out
}

func backendLabel(mon ipc.MonitorStatus) string {
	if mon.BrightnessBackend == "ddc" {
		return "ddc"
	}
	return "software"
}

func stateLabel(mon ipc.MonitorStatus) string {
	if !mon.Online {
		return "offline"
	}
	return "online"
}
