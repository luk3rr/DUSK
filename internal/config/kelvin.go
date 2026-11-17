// Filename: internal/config/kelvin.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package config

import "math"

// Color temperature range of night mode. NeutralKelvin changes nothing.
const (
	MinKelvin     = 1700
	NeutralKelvin = 6500
)

// kelvinRGB approximates the color of a black body at k kelvin (0-255 per channel).
func kelvinRGB(k int) (r, g, b float64) {
	t := float64(k) / 100
	clamp := func(v float64) float64 { return math.Max(0, math.Min(255, v)) }
	if t <= 66 {
		r = 255
		g = 99.4708025861*math.Log(t) - 161.1195681661
	} else {
		r = 329.698727446 * math.Pow(t-60, -0.1332047592)
		g = 288.1221695283 * math.Pow(t-60, -0.0755148492)
	}
	switch {
	case t >= 66:
		b = 255
	case t <= 19:
		b = 0
	default:
		b = 138.5177312231*math.Log(t-10) - 305.0447927307
	}
	return clamp(r), clamp(g), clamp(b)
}

// KelvinForGains returns the color temperature whose gains are closest to the given
// ones (percent of full). Gains that change nothing give NeutralKelvin. It is how the
// interface tells the current warmth, whatever mix of channels is applied.
func KelvinForGains(r, g, b int) int {
	if r >= 100 && g >= 100 && b >= 100 {
		return NeutralKelvin
	}
	best, bestD := NeutralKelvin, 1<<30
	for k := NeutralKelvin; k >= MinKelvin; k -= 50 {
		kr, kg, kb := GainsForKelvin(k)
		d := abs(kr-r) + 2*abs(kg-g) + 2*abs(kb-b)
		if d < bestD {
			best, bestD = k, d
		}
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// GainsForKelvin returns the red, green and blue gains, in percent of full,
// that give the screen the color of a light at k kelvin. NeutralKelvin gives
// 100, 100, 100; lower values remove blue first, then green. k is clamped to
// [MinKelvin, NeutralKelvin].
func GainsForKelvin(k int) (r, g, b int) {
	k = max(MinKelvin, min(NeutralKelvin, k))
	rr, gg, bb := kelvinRGB(k)
	r0, g0, b0 := kelvinRGB(NeutralKelvin)
	pct := func(v, ref float64) int {
		return max(0, min(100, int(math.Round(100*v/ref))))
	}
	return pct(rr, r0), pct(gg, g0), pct(bb, b0)
}
