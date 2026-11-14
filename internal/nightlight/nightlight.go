// Package nightlight controls the desktop's night light. It is only the
// fallback for monitors without RGB gain control (ADR-3, ADR-8). The only
// backend today is GNOME's, driven through gsettings.
package nightlight

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"dusk/internal/ddc"
	"dusk/internal/schedule"
)

const schema = "org.gnome.settings-daemon.plugins.color"

// ErrNoBackend means no supported night-light backend was found.
var ErrNoBackend = errors.New("no supported night-light backend found")

// Original holds the user's settings before dusk took control.
type Original struct {
	Enabled     bool    `json:"enabled"`
	Automatic   bool    `json:"automatic"`
	From        float64 `json:"from"`
	To          float64 `json:"to"`
	Temperature int     `json:"temperature"`
}

// Backend is what the controller needs from a night light.
type Backend interface {
	// Snapshot reads the current settings.
	Snapshot(ctx context.Context) (Original, error)
	// Enable turns the night light on at the given temperature, under dusk's control.
	Enable(ctx context.Context, kelvin int) error
	// Restore puts the saved settings back exactly.
	Restore(ctx context.Context, o Original) error
}

// Detect looks for a supported backend. gsettings is the Runner for the
// gsettings binary (for example ddc.ExecRunner{Path: "gsettings"}). It returns
// ErrNoBackend (wrapped with the reason) when none is usable.
func Detect(ctx context.Context, gsettings ddc.Runner) (Backend, error) {
	g := Gsettings{Runner: gsettings}
	if _, err := g.get(ctx, "night-light-enabled"); err != nil {
		return nil, fmt.Errorf("%w: GNOME night light unavailable: %v", ErrNoBackend, err)
	}
	return g, nil
}

// Gsettings implements Backend by calling the gsettings binary through a Runner.
type Gsettings struct {
	Runner ddc.Runner // for example ddc.ExecRunner{Path: "gsettings"}
}

var _ Backend = Gsettings{}

func (g Gsettings) get(ctx context.Context, key string) (string, error) {
	out, err := g.Runner.Run(ctx, "get", schema, key)
	if err != nil {
		return "", fmt.Errorf("gsettings get %s: %w", key, err)
	}
	return strings.TrimSpace(out), nil
}

func (g Gsettings) set(ctx context.Context, key, value string) error {
	if _, err := g.Runner.Run(ctx, "set", schema, key, value); err != nil {
		return fmt.Errorf("gsettings set %s %s: %w", key, value, err)
	}
	return nil
}

// Snapshot implements Backend.
func (g Gsettings) Snapshot(ctx context.Context) (Original, error) {
	var o Original
	var raw [5]string
	for i, key := range []string{"night-light-enabled", "night-light-schedule-automatic", "night-light-schedule-from", "night-light-schedule-to", "night-light-temperature"} {
		v, err := g.get(ctx, key)
		if err != nil {
			return o, err
		}
		raw[i] = v
	}
	var err error
	if o.Enabled, err = strconv.ParseBool(raw[0]); err != nil {
		return o, fmt.Errorf("night-light-enabled: %q", raw[0])
	}
	if o.Automatic, err = strconv.ParseBool(raw[1]); err != nil {
		return o, fmt.Errorf("night-light-schedule-automatic: %q", raw[1])
	}
	if o.From, err = strconv.ParseFloat(raw[2], 64); err != nil {
		return o, fmt.Errorf("night-light-schedule-from: %q", raw[2])
	}
	if o.To, err = strconv.ParseFloat(raw[3], 64); err != nil {
		return o, fmt.Errorf("night-light-schedule-to: %q", raw[3])
	}
	if o.Temperature, err = strconv.Atoi(strings.TrimPrefix(raw[4], "uint32 ")); err != nil {
		return o, fmt.Errorf("night-light-temperature: %q", raw[4])
	}
	return o, nil
}

func hours(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// Enable implements Backend. It turns off GNOME's automatic schedule and keeps
// the night light active all day (from 0.0 to 24.0) at the given temperature
// (A-17).
func (g Gsettings) Enable(ctx context.Context, kelvin int) error {
	kelvin = max(schedule.MinKelvin, min(schedule.NeutralKelvin, kelvin))
	steps := [][2]string{
		{"night-light-temperature", strconv.Itoa(kelvin)},
		{"night-light-schedule-from", "0.0"},
		{"night-light-schedule-to", "24.0"},
		{"night-light-schedule-automatic", "false"},
		{"night-light-enabled", "true"},
	}
	for _, s := range steps {
		if err := g.set(ctx, s[0], s[1]); err != nil {
			return err
		}
	}
	return nil
}

// Restore implements Backend. `enabled` is the last key written.
func (g Gsettings) Restore(ctx context.Context, o Original) error {
	steps := [][2]string{
		{"night-light-temperature", strconv.Itoa(o.Temperature)},
		{"night-light-schedule-from", hours(o.From)},
		{"night-light-schedule-to", hours(o.To)},
		{"night-light-schedule-automatic", strconv.FormatBool(o.Automatic)},
		{"night-light-enabled", strconv.FormatBool(o.Enabled)},
	}
	for _, s := range steps {
		if err := g.set(ctx, s[0], s[1]); err != nil {
			return err
		}
	}
	return nil
}
