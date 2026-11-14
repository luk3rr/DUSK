// Package ddctest provides fakes for testing without touching real hardware.
package ddctest

import (
	"context"
	"fmt"
	"sync"

	"dusk/internal/ddc"
)

// FakeRunner records calls and answers through Handler.
type FakeRunner struct {
	Handler func(args []string) (string, error)

	mu    sync.Mutex
	calls [][]string
}

// Run implements ddc.Runner.
func (f *FakeRunner) Run(_ context.Context, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()
	if f.Handler == nil {
		return "", fmt.Errorf("ddctest: no Handler")
	}
	return f.Handler(args)
}

// Calls returns a copy of every call made.
func (f *FakeRunner) Calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.calls...)
}

// SetCalls returns only the write calls (setvcp).
func (f *FakeRunner) SetCalls() [][]string {
	var out [][]string
	for _, c := range f.Calls() {
		if len(c) > 0 && c[0] == "setvcp" {
			out = append(out, c)
		}
	}
	return out
}

// Write is a write recorded by FakeDisplay.
type Write struct {
	VCP   ddc.VCP
	Value int
}

// FakeDisplay is an in-memory ddc.Display.
type FakeDisplay struct {
	DisplayInfo ddc.Info
	DisplayCaps ddc.Caps
	// OnSet, when set, runs before every valid write (without holding locks).
	OnSet func(vcp ddc.VCP, v int)

	mu      sync.Mutex
	failSet error
	tries   int
	gets    int
	values  map[ddc.VCP]int
	writes  []Write
}

// SetFailure makes subsequent writes fail with err (nil restores normal behavior).
func (f *FakeDisplay) SetFailure(err error) {
	f.mu.Lock()
	f.failSet = err
	f.mu.Unlock()
}

// SetValue changes a value without recording a write, simulating the
// monitor's own buttons.
func (f *FakeDisplay) SetValue(vcp ddc.VCP, v int) {
	f.mu.Lock()
	f.values[vcp] = v
	f.mu.Unlock()
}

// SetAttempts counts calls to Set with an allowed VCP, including failed ones.
func (f *FakeDisplay) SetAttempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tries
}

// Gets counts the reads made, to show how chatty a caller is on the bus.
func (f *FakeDisplay) Gets() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

// Value returns the current value of a VCP.
func (f *FakeDisplay) Value(vcp ddc.VCP) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[vcp]
}

// NewFakeDisplay creates a monitor with brightness and RGB at 100 (max 100).
func NewFakeDisplay(info ddc.Info) *FakeDisplay {
	return &FakeDisplay{
		DisplayInfo: info,
		DisplayCaps: ddc.Caps{Brightness: true, BrightnessMax: 100, RGB: true, RGBMax: [3]int{100, 100, 100}},
		values: map[ddc.VCP]int{
			ddc.VCPBrightness: 100, ddc.VCPRedGain: 100, ddc.VCPGreenGain: 100, ddc.VCPBlueGain: 100,
		},
	}
}

func (f *FakeDisplay) Info() ddc.Info { return f.DisplayInfo }
func (f *FakeDisplay) Caps() ddc.Caps { return f.DisplayCaps }

// Get returns the stored value and a max of 100.
func (f *FakeDisplay) Get(_ context.Context, vcp ddc.VCP) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	v, ok := f.values[vcp]
	if !ok {
		return 0, 0, ddc.ErrUnsupported
	}
	return v, 100, nil
}

// Set applies the whitelist and clamping like the real Device and records the write.
func (f *FakeDisplay) Set(_ context.Context, vcp ddc.VCP, v int) error {
	if !vcp.Allowed() {
		return ddc.ErrVCPNotAllowed
	}
	f.mu.Lock()
	f.tries++
	fail := f.failSet
	f.mu.Unlock()
	if fail != nil {
		return fail
	}
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	if f.OnSet != nil {
		f.OnSet(vcp, v)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[vcp] = v
	f.writes = append(f.writes, Write{VCP: vcp, Value: v})
	return nil
}

// Writes returns a copy of the writes made.
func (f *FakeDisplay) Writes() []Write {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Write(nil), f.writes...)
}

var _ ddc.Display = (*FakeDisplay)(nil)
