// Package schedule holds the pure scheduling functions: target brightness,
// night-mode intensity and stepping. None of them performs I/O.
package schedule

import (
	"math"
	"time"

	"dusk/internal/config"
)

const minutesPerDay = 1440.0

// minuteOfDay returns the (fractional) minutes since 00:00 on t's wall clock.
func minuteOfDay(t time.Time) float64 {
	return float64(t.Hour()*60+t.Minute()) + float64(t.Second())/60 + float64(t.Nanosecond())/6e10
}

// Target returns the monitor's target brightness (0-100) at t, interpolating
// linearly and circularly between the key points. The second result is false
// when the monitor has no points.
func Target(m config.Monitor, t time.Time) (int, bool) {
	pts := m.Points
	n := len(pts)
	switch n {
	case 0:
		return 0, false
	case 1:
		return pts[0].Brightness, true
	}
	now := minuteOfDay(t)

	prev := -1 // last point with Minute <= now
	for i, p := range pts {
		if float64(p.Minute) <= now {
			prev = i
		}
	}
	var p1, p2 config.Point
	var m1, m2 float64
	if prev < 0 { // before the first point of the day: coming from yesterday's last
		p1, p2 = pts[n-1], pts[0]
		m1, m2 = float64(p1.Minute)-minutesPerDay, float64(p2.Minute)
	} else {
		p1, p2 = pts[prev], pts[(prev+1)%n]
		m1, m2 = float64(p1.Minute), float64(p2.Minute)
		if prev == n-1 {
			m2 += minutesPerDay
		}
	}
	frac := (now - m1) / (m2 - m1)
	b := float64(p1.Brightness) + float64(p2.Brightness-p1.Brightness)*frac
	return clamp(int(math.Round(b)), 0, 100), true
}

// NightIntensity returns the night-mode intensity at t, from 0 to 1. It rises
// over [start, start+ramp], stays at 1 until end, and falls over [end, end+ramp].
func NightIntensity(n config.Night, t time.Time) float64 {
	if !n.Enabled {
		return 0
	}
	ramp := n.Ramp.Minutes()
	window := mod(float64(n.End-n.Start), minutesPerDay)
	off := mod(minuteOfDay(t)-float64(n.Start), minutesPerDay)
	switch {
	case off < ramp:
		return off / ramp
	case off <= window:
		return 1
	case off < window+ramp:
		return 1 - (off-window)/ramp
	default:
		return 0
	}
}

// Phase is where night mode currently is in its daily cycle.
type Phase string

// Night-mode phases.
const (
	PhaseOff       Phase = "off"
	PhaseDay       Phase = "day"
	PhaseFadingIn  Phase = "fading in"
	PhaseNight     Phase = "night"
	PhaseFadingOut Phase = "fading out"
)

// NightPhase returns the current phase of night mode and the instant it changes
// to the next one. With night mode disabled it returns PhaseOff and a zero time.
func NightPhase(n config.Night, t time.Time) (Phase, time.Time) {
	if !n.Enabled {
		return PhaseOff, time.Time{}
	}
	ramp := n.Ramp.Minutes()
	window := mod(float64(n.End-n.Start), minutesPerDay)
	off := mod(minuteOfDay(t)-float64(n.Start), minutesPerDay)
	var phase Phase
	var left float64 // minutes until the next phase
	switch {
	case off < ramp:
		phase, left = PhaseFadingIn, ramp-off
	case off <= window:
		phase, left = PhaseNight, window-off
	case off < window+ramp:
		phase, left = PhaseFadingOut, window+ramp-off
	default:
		phase, left = PhaseDay, minutesPerDay-off
	}
	return phase, t.Add(time.Duration(left * float64(time.Minute))).Truncate(time.Second)
}

// NightGains returns the red, green and blue multipliers (in percent) for the
// given intensity: 100 when night mode is off, the configured gain at its peak.
func NightGains(n config.Night, intensity float64) (r, g, b int) {
	intensity = math.Max(0, math.Min(1, intensity))
	gain := func(target int) int {
		return clamp(int(math.Round(100-intensity*float64(100-target))), 0, 100)
	}
	return gain(n.RedGain), gain(n.GreenGain), gain(n.BlueGain)
}

// Color temperature limits of the GNOME Night Light.
const (
	NeutralKelvin = 4700
	MinKelvin     = 1700
)

// NightLightKelvin returns the color temperature for the given intensity:
// 4700 K (neutral) at intensity 0 down to peak at intensity 1. peak is clamped
// to 1700-4700.
func NightLightKelvin(intensity float64, peak int) int {
	peak = clamp(peak, MinKelvin, NeutralKelvin)
	intensity = math.Max(0, math.Min(1, intensity))
	return int(math.Round(NeutralKelvin - intensity*float64(NeutralKelvin-peak)))
}

// Step moves current toward target by at most max points and returns the new
// value. A result equal to current means there is nothing to write.
func Step(current, target, max int) int {
	if max < 1 {
		max = 1
	}
	switch {
	case target > current+max:
		return current + max
	case target < current-max:
		return current - max
	default:
		return target
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func mod(x, m float64) float64 {
	r := math.Mod(x, m)
	if r < 0 {
		r += m
	}
	return r
}
