package config

import (
	"fmt"
	"strings"
	"time"
)

// Marshal renders cfg as commented TOML that Parse reads back into an equal
// Config. It expects a normalized config (as produced by Parse or by the setup
// wizard). notes maps a monitor name to free text written as comments above
// its entry.
func Marshal(cfg *Config, notes map[string]string) []byte {
	var b strings.Builder
	b.WriteString("# dusk configuration.\n")
	b.WriteString("# Edit it by hand or run `dusk setup` again. `dusk check-config` validates it.\n\n")
	fmt.Fprintf(&b, "poll_interval   = %s # how often dusk re-checks the targets\n", tomlString(FormatDuration(cfg.PollInterval)))
	fmt.Fprintf(&b, "rescan_interval = %s # how often it looks for new or returning monitors\n", tomlString(FormatDuration(cfg.RescanInterval)))
	verify := cfg.VerifyInterval
	if verify <= 0 {
		verify = DefaultVerifyInterval
	}
	fmt.Fprintf(&b, "verify_interval = %s # how often it re-reads the monitors to notice manual changes (less often means fewer pauses)\n\n", tomlString(FormatDuration(verify)))

	n := cfg.Night
	ramp := n.Ramp
	if ramp <= 0 {
		ramp = DefaultRamp
	}
	fallback := n.Fallback
	if fallback == "" {
		fallback = DefaultFallback
	}
	b.WriteString("# Night mode warms the screen colors (less blue) and fades in and out gradually.\n")
	b.WriteString("[night]\n")
	if n.Enabled {
		b.WriteString("enabled    = true\n")
		fmt.Fprintf(&b, "start      = %s\n", tomlString(FormatClock(n.Start)))
		fmt.Fprintf(&b, "end        = %s\n", tomlString(FormatClock(n.End)))
	} else {
		b.WriteString("enabled    = false\n")
	}
	fmt.Fprintf(&b, "ramp       = %s # how long the fade in and the fade out take\n", tomlString(FormatDuration(ramp)))
	fmt.Fprintf(&b, "blue_gain  = %d # percent of the original blue at the peak (lower is warmer)\n", n.BlueGain)
	fmt.Fprintf(&b, "red_gain   = %d\n", n.RedGain)
	fmt.Fprintf(&b, "green_gain = %d\n", n.GreenGain)
	fmt.Fprintf(&b, "fallback   = %s # \"auto\" uses the desktop night light for monitors without color control, \"off\" never does\n", tomlString(fallback))

	for _, m := range cfg.Monitors {
		b.WriteString("\n")
		if note := notes[m.Name]; note != "" {
			for _, line := range strings.Split(note, "\n") {
				fmt.Fprintf(&b, "# %s\n", line)
			}
		}
		b.WriteString("[[monitor]]\n")
		fmt.Fprintf(&b, "name  = %s\n", tomlString(m.Name))
		fmt.Fprintf(&b, "match = %s # \"model\" or \"model#serial\"\n", tomlString(m.Match))
		if !m.Night.IsZero() {
			var parts []string
			for _, kv := range []struct {
				k string
				v *int
			}{{"blue_gain", m.Night.BlueGain}, {"red_gain", m.Night.RedGain}, {"green_gain", m.Night.GreenGain}} {
				if kv.v != nil {
					parts = append(parts, fmt.Sprintf("%s = %d", kv.k, *kv.v))
				}
			}
			fmt.Fprintf(&b, "night = { %s } # this monitor's own night color level\n", strings.Join(parts, ", "))
		}
		b.WriteString("points = [\n")
		for _, p := range m.Points {
			fmt.Fprintf(&b, "  { time = %s, brightness = %d },\n", tomlString(FormatClock(p.Minute)), p.Brightness)
		}
		b.WriteString("]\n")
	}
	return []byte(b.String())
}

// FormatDuration renders d in the shortest form time.ParseDuration accepts.
func FormatDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
