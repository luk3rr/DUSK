// Filename: internal/monitors/resolve.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package monitors

import (
	"regexp"
	"strings"

	"dusk/internal/config"
	"dusk/internal/ddc"
	"dusk/internal/gamma"
)

// Resolved ties a screen (the anchor) to its configuration entry and to its
// DDC display. Monitor is nil when the screen has no entry (no schedule).
// Display is nil when the monitor has no DDC/CI (it is still controllable by gamma).
type Resolved struct {
	Output  gamma.Output
	Monitor *config.Monitor
	Display ddc.Display
}

var cardPrefix = regexp.MustCompile(`^card\d+-`)

// NormalizeConnector turns a DRM connector ("card1-HDMI-A-1") and a Mutter
// output name ("HDMI-1") into the same lower-case form ("hdmi-1").
func NormalizeConnector(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = cardPrefix.ReplaceAllString(s, "")
	return strings.Replace(s, "hdmi-a-", "hdmi-", 1)
}

// Resolve matches every output with its configuration entry and DDC display.
// Config: "model#serial" (only when the output has a serial) wins over
// "model"; comparison ignores case. DDC: by normalized connector first, then
// by model plus serial (equal, or empty on either side) when that is unambiguous.
func Resolve(cfg *config.Config, outputs []gamma.Output, displays []ddc.Display) []Resolved {
	byMatch := map[string]*config.Monitor{}
	if cfg != nil {
		for i := range cfg.Monitors {
			byMatch[strings.ToLower(cfg.Monitors[i].Match)] = &cfg.Monitors[i]
		}
	}
	taken := map[ddc.Display]bool{}
	out := make([]Resolved, 0, len(outputs))
	for _, o := range outputs {
		r := Resolved{Output: o}
		if o.Serial != "" {
			r.Monitor = byMatch[strings.ToLower(o.Key())]
		}
		if r.Monitor == nil {
			r.Monitor = byMatch[strings.ToLower(o.Product)]
		}
		out = append(out, r)
	}
	// Pass 1: connector names.
	for i := range out {
		want := NormalizeConnector(out[i].Output.Name)
		for _, d := range displays {
			if !taken[d] && want != "" && NormalizeConnector(d.Info().Connector) == want {
				out[i].Display, taken[d] = d, true
				break
			}
		}
	}
	// Pass 2: model and serial, for outputs still without a display.
	for i := range out {
		if out[i].Display != nil {
			continue
		}
		var found []ddc.Display
		for _, d := range displays {
			info := d.Info()
			if taken[d] || !strings.EqualFold(info.Model, out[i].Output.Product) {
				continue
			}
			if info.Serial == "" || out[i].Output.Serial == "" || strings.EqualFold(info.Serial, out[i].Output.Serial) {
				found = append(found, d)
			}
		}
		if len(found) == 1 {
			out[i].Display, taken[found[0]] = found[0], true
		}
	}
	return out
}
