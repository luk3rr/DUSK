// Filename: internal/gamma/gamma.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

// Package gamma builds and applies software gamma ramps. Colors and
// brightness live in the ramp, never in the monitor's own memory.
package gamma

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// Errors returned by backends.
var (
	ErrNoBackend   = errors.New("gamma: no supported backend found")
	ErrOutputGone  = errors.New("gamma: output is gone")
	ErrInvalidRamp = errors.New("gamma: invalid ramp")
	ErrStale       = errors.New("gamma: stale display configuration")
)

// Ramp is a gamma lookup table with one entry per step for each channel.
type Ramp struct {
	R, G, B []uint16
}

// Len returns the number of entries per channel.
func (r Ramp) Len() int { return len(r.R) }

// Valid reports whether the three channels have the same size (at least 2)
// and never decrease.
func (r Ramp) Valid() error {
	n := len(r.R)
	if n < 2 || len(r.G) != n || len(r.B) != n {
		return fmt.Errorf("%w: channel sizes %d/%d/%d", ErrInvalidRamp, len(r.R), len(r.G), len(r.B))
	}
	for _, ch := range [][]uint16{r.R, r.G, r.B} {
		for i := 1; i < n; i++ {
			if ch[i] < ch[i-1] {
				return fmt.Errorf("%w: not monotonic at %d", ErrInvalidRamp, i)
			}
		}
	}
	return nil
}

// Equal reports whether two ramps hold the same values.
func (r Ramp) Equal(o Ramp) bool {
	eq := func(a, b []uint16) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	return eq(r.R, o.R) && eq(r.G, o.G) && eq(r.B, o.B)
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func channel(n int, factor float64) []uint16 {
	out := make([]uint16, n)
	for i := range out {
		out[i] = uint16(math.Round(float64(i) * 65535 / float64(n-1) * factor))
	}
	return out
}

// Identity returns the linear ramp with n entries per channel (n >= 2).
func Identity(n int) Ramp {
	return Build(n, 1, 1, 1, 1)
}

// Build returns the identity ramp multiplied by a gain per channel and by a
// common scale. All factors are clamped to [0, 1].
func Build(n int, r, g, b, scale float64) Ramp {
	if n < 2 {
		n = 2
	}
	s := clamp01(scale)
	return Ramp{
		R: channel(n, clamp01(r)*s),
		G: channel(n, clamp01(g)*s),
		B: channel(n, clamp01(b)*s),
	}
}

// BrightnessScale maps a brightness percentage to a ramp scale in [0, 1]:
// 0% gives the floor, 100% gives 1.
func BrightnessScale(percent, floor int) float64 {
	p := float64(max(0, min(100, percent)))
	f := float64(max(0, min(100, floor)))
	return (f + (100-f)*p/100) / 100
}

// Output is a monitor that accepts a gamma ramp.
type Output struct {
	CRTC    uint32
	Name    string // connector, for example "DP-1"
	Vendor  string
	Product string // model
	Serial  string // empty when the monitor reports none
}

// Key returns "product#serial", or just "product" when there is no serial.
func (o Output) Key() string {
	if o.Serial == "" {
		return o.Product
	}
	return o.Product + "#" + o.Serial
}

// Backend reads and writes gamma ramps.
type Backend interface {
	// Outputs lists the active outputs.
	Outputs(ctx context.Context) ([]Output, error)
	// Get reads the current ramp of an output.
	Get(ctx context.Context, o Output) (Ramp, error)
	// Set writes a ramp. It never sends an invalid ramp.
	Set(ctx context.Context, o Output, r Ramp) error
	// Watch sends on ch (without blocking) whenever the monitors change
	// (hotplug, resume). It returns when ctx is done.
	Watch(ctx context.Context, ch chan<- struct{}) error
}

// Reset applies the identity ramp to every output. It keeps going after a
// failure and returns all errors joined.
func Reset(ctx context.Context, b Backend) error {
	outs, err := b.Outputs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, o := range outs {
		cur, err := b.Get(ctx, o)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", o.Name, err))
			continue
		}
		if err := b.Set(ctx, o, Identity(cur.Len())); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", o.Name, err))
		}
	}
	return errors.Join(errs...)
}
