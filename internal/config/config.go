// Package config reads and validates dusk's TOML configuration file.
package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Defaults.
const (
	DefaultPollInterval   = 30 * time.Second
	DefaultRescanInterval = 30 * time.Minute
	DefaultVerifyInterval = 5 * time.Minute
	DefaultRamp           = 30 * time.Minute
	DefaultBlueGain       = 60
	DefaultFallback       = FallbackAuto
)

// Valid values of night.fallback.
const (
	// FallbackAuto uses whatever night-light backend is detected at run time.
	FallbackAuto = "auto"
	// FallbackOff never touches the desktop's night light.
	FallbackOff = "off"
)

// Config is the validated configuration. It is immutable by convention.
type Config struct {
	PollInterval   time.Duration
	RescanInterval time.Duration
	// VerifyInterval is how often the monitors are read back to notice manual changes.
	// Between checks dusk relies on the values it knows, which keeps the I2C bus quiet.
	VerifyInterval time.Duration
	Night          Night
	Monitors       []Monitor
}

// Night describes night mode. Start and End are minutes since 00:00.
type Night struct {
	Enabled   bool
	Start     int
	End       int
	Ramp      time.Duration
	BlueGain  int
	RedGain   int
	GreenGain int
	Fallback  string
}

// Monitor ties a monitor (by Match) to its brightness schedule.
type Monitor struct {
	Name   string
	Match  string // "model" or "model#serial"
	Points []Point
	// Night overrides the global night-mode color gains for this monitor only.
	Night MonitorNight
}

// MonitorNight holds optional per-monitor color gains (percent of the original
// gain at the peak of night mode). A nil field inherits the value of [night].
type MonitorNight struct {
	BlueGain  *int
	RedGain   *int
	GreenGain *int
}

// IsZero reports whether nothing is overridden.
func (n MonitorNight) IsZero() bool {
	return n.BlueGain == nil && n.RedGain == nil && n.GreenGain == nil
}

// NightFor returns base with this monitor's color-gain overrides applied. The
// window, ramp and fallback always come from base.
func (m Monitor) NightFor(base Night) Night {
	if m.Night.BlueGain != nil {
		base.BlueGain = *m.Night.BlueGain
	}
	if m.Night.RedGain != nil {
		base.RedGain = *m.Night.RedGain
	}
	if m.Night.GreenGain != nil {
		base.GreenGain = *m.Night.GreenGain
	}
	return base
}

// Point is a key point: time of day to brightness. Minute is 0 to 1439.
type Point struct {
	Minute     int
	Brightness int
}

// ValidationError aggregates every problem found in a configuration.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid configuration:\n  - " + strings.Join(e.Problems, "\n  - ")
}

type rawConfig struct {
	PollInterval   string       `toml:"poll_interval"`
	RescanInterval string       `toml:"rescan_interval"`
	VerifyInterval string       `toml:"verify_interval"`
	Night          *rawNight    `toml:"night"`
	Monitors       []rawMonitor `toml:"monitor"`
}

type rawNight struct {
	Enabled   *bool  `toml:"enabled"`
	Start     string `toml:"start"`
	End       string `toml:"end"`
	Ramp      string `toml:"ramp"`
	BlueGain  *int   `toml:"blue_gain"`
	RedGain   *int   `toml:"red_gain"`
	GreenGain *int   `toml:"green_gain"`
	Fallback  string `toml:"fallback"`
}

type rawMonitor struct {
	Name   string           `toml:"name"`
	Match  string           `toml:"match"`
	Night  *rawMonitorNight `toml:"night"`
	Points []rawPoint       `toml:"points"`
}

type rawMonitorNight struct {
	BlueGain  *int `toml:"blue_gain"`
	RedGain   *int `toml:"red_gain"`
	GreenGain *int `toml:"green_gain"`
}

type rawPoint struct {
	Time       string `toml:"time"`
	Brightness *int   `toml:"brightness"`
}

// Load reads and validates the file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse validates TOML content. On failure it returns a *ValidationError
// listing every problem, or the TOML syntax error.
func Parse(data []byte) (*Config, error) {
	var raw rawConfig
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, fmt.Errorf("invalid TOML: %w", err)
	}
	v := &validator{}
	for _, k := range md.Undecoded() {
		v.addf("unknown key: %s", k.String())
	}
	cfg := &Config{}
	cfg.PollInterval = v.duration("poll_interval", raw.PollInterval, DefaultPollInterval)
	cfg.RescanInterval = v.duration("rescan_interval", raw.RescanInterval, DefaultRescanInterval)
	cfg.VerifyInterval = v.duration("verify_interval", raw.VerifyInterval, DefaultVerifyInterval)
	cfg.Night = v.night(raw.Night)
	cfg.Monitors = v.monitors(raw.Monitors)
	if len(v.problems) > 0 {
		return nil, &ValidationError{Problems: v.problems}
	}
	return cfg, nil
}

// IsValidation reports whether err is a *ValidationError.
func IsValidation(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

type validator struct {
	problems []string
}

func (v *validator) addf(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

func (v *validator) duration(field, s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		v.addf("%s: invalid duration %q", field, s)
		return def
	}
	if d <= 0 {
		v.addf("%s: duration must be positive, got %q", field, s)
		return def
	}
	return d
}

// ParseClock converts "HH:MM" into minutes since 00:00.
func ParseClock(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, fmt.Errorf("time %q is not in HH:MM format", s)
	}
	digit := func(c byte) bool { return c >= '0' && c <= '9' }
	if !digit(s[0]) || !digit(s[1]) || !digit(s[3]) || !digit(s[4]) {
		return 0, fmt.Errorf("time %q is not in HH:MM format", s)
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h > 23 || m > 59 {
		return 0, fmt.Errorf("time %q is outside 00:00-23:59", s)
	}
	return h*60 + m, nil
}

// FormatClock is the inverse of ParseClock.
func FormatClock(minute int) string { return fmt.Sprintf("%02d:%02d", minute/60, minute%60) }

func (v *validator) clock(field, s string) (int, bool) {
	m, err := ParseClock(s)
	if err != nil {
		v.addf("%s: %v", field, err)
		return 0, false
	}
	return m, true
}

func (v *validator) percent(field string, p *int, def int) int {
	if p == nil {
		return def
	}
	if *p < 0 || *p > 100 {
		v.addf("%s: %d is outside 0-100", field, *p)
		return def
	}
	return *p
}

// optPercent validates an optional percentage: nil stays nil.
func (v *validator) optPercent(field string, p *int) *int {
	if p == nil {
		return nil
	}
	if *p < 0 || *p > 100 {
		v.addf("%s: %d is outside 0-100", field, *p)
		return nil
	}
	n := *p
	return &n
}

func (v *validator) night(r *rawNight) Night {
	n := Night{
		Ramp:      DefaultRamp,
		BlueGain:  DefaultBlueGain,
		RedGain:   100,
		GreenGain: 100,
		Fallback:  DefaultFallback,
	}
	if r == nil {
		return n // no [night] table: night mode is off
	}
	n.Enabled = r.Enabled == nil || *r.Enabled
	n.Ramp = v.duration("night.ramp", r.Ramp, DefaultRamp)
	n.BlueGain = v.percent("night.blue_gain", r.BlueGain, DefaultBlueGain)
	n.RedGain = v.percent("night.red_gain", r.RedGain, 100)
	n.GreenGain = v.percent("night.green_gain", r.GreenGain, 100)
	switch r.Fallback {
	case "":
	case FallbackAuto, FallbackOff:
		n.Fallback = r.Fallback
	default:
		v.addf("night.fallback: unknown value %q (use %q or %q)", r.Fallback, FallbackAuto, FallbackOff)
	}
	if !n.Enabled {
		return n
	}
	start, okS := v.clock("night.start", r.Start)
	end, okE := v.clock("night.end", r.End)
	if !okS || !okE {
		return n
	}
	n.Start, n.End = start, end
	if start == end {
		v.addf("night: start and end must differ")
		return n
	}
	window := float64(((end - start) + 1440) % 1440)
	ramp := n.Ramp.Minutes()
	if ramp > window {
		v.addf("night.ramp: %s is longer than the night window (%.0f min)", n.Ramp, window)
	}
	if ramp > 1440-window {
		v.addf("night.ramp: %s runs into the next ramp-up (only %.0f min outside the window)", n.Ramp, 1440-window)
	}
	return n
}

func (v *validator) monitors(raws []rawMonitor) []Monitor {
	var out []Monitor
	names := map[string]bool{}
	matches := map[string]bool{}
	for i, r := range raws {
		field := fmt.Sprintf("monitor[%d]", i)
		if r.Name != "" {
			field = fmt.Sprintf("monitor %q", r.Name)
		}
		m := Monitor{Name: r.Name, Match: r.Match}
		switch {
		case r.Name == "":
			v.addf("%s: name is required", field)
		case names[r.Name]:
			v.addf("%s: duplicate name", field)
		default:
			names[r.Name] = true
		}
		switch {
		case r.Match == "":
			v.addf("%s: match is required", field)
		case matches[r.Match]:
			v.addf("%s: duplicate match %q", field, r.Match)
		default:
			matches[r.Match] = true
		}
		if r.Night != nil {
			m.Night = MonitorNight{
				BlueGain:  v.optPercent(field+".night.blue_gain", r.Night.BlueGain),
				RedGain:   v.optPercent(field+".night.red_gain", r.Night.RedGain),
				GreenGain: v.optPercent(field+".night.green_gain", r.Night.GreenGain),
			}
		}
		if len(r.Points) == 0 {
			v.addf("%s: needs at least one entry in points", field)
		}
		seen := map[int]bool{}
		for j, rp := range r.Points {
			pf := fmt.Sprintf("%s.points[%d]", field, j)
			minute, ok := v.clock(pf+".time", rp.Time)
			if rp.Brightness == nil {
				v.addf("%s.brightness is required", pf)
				continue
			}
			b := *rp.Brightness
			if b < 0 || b > 100 {
				v.addf("%s.brightness: %d is outside 0-100", pf, b)
				continue
			}
			if !ok {
				continue
			}
			if seen[minute] {
				v.addf("%s: time %s is repeated", pf, rp.Time)
				continue
			}
			seen[minute] = true
			m.Points = append(m.Points, Point{Minute: minute, Brightness: b})
		}
		sort.Slice(m.Points, func(a, b int) bool { return m.Points[a].Minute < m.Points[b].Minute })
		out = append(out, m)
	}
	return out
}
