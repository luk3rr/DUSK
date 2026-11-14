// Package ddc talks to DDC/CI monitors through ddcutil. Every write goes
// through a VCP whitelist and value clamping.
package ddc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// VCP is an MCCS feature code.
type VCP byte

// VCPs that dusk is allowed to write.
const (
	VCPBrightness VCP = 0x10
	VCPRedGain    VCP = 0x16
	VCPGreenGain  VCP = 0x18
	VCPBlueGain   VCP = 0x1A
)

var probed = []VCP{VCPBrightness, VCPRedGain, VCPGreenGain, VCPBlueGain}

// Allowed reports whether v may be written.
func (v VCP) Allowed() bool {
	for _, a := range probed {
		if v == a {
			return true
		}
	}
	return false
}

func (v VCP) hex() string { return fmt.Sprintf("%02X", byte(v)) }

func (v VCP) String() string { return "0x" + v.hex() }

// Errors returned by Set.
var (
	ErrVCPNotAllowed = errors.New("ddc: VCP is not in the write whitelist")
	ErrUnsupported   = errors.New("ddc: monitor does not support this VCP")
)

// Runner runs ddcutil with the given arguments and returns its standard output.
type Runner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

// Info identifies a monitor.
type Info struct {
	Mfg       string
	Model     string
	Serial    string // may be empty
	Bus       int
	Connector string
}

// Key returns "model#serial", or just "model" when there is no serial.
func (i Info) Key() string {
	if i.Serial == "" {
		return i.Model
	}
	return i.Model + "#" + i.Serial
}

// Caps describes what a monitor accepts, discovered by read probing.
type Caps struct {
	Brightness    bool
	BrightnessMax int
	RGB           bool // 0x16, 0x18 and 0x1A all respond
	RGBMax        [3]int
}

// Display is a controllable monitor.
type Display interface {
	Info() Info
	Caps() Caps
	Get(ctx context.Context, vcp VCP) (cur, max int, err error)
	Set(ctx context.Context, vcp VCP, v int) error
}

// Device is the real Display, backed by a Runner.
type Device struct {
	runner Runner
	info   Info

	mu   sync.Mutex // serializes calls to the same bus
	caps Caps
	max  map[VCP]int
}

// NewDevice creates a Device that has not been probed yet.
func NewDevice(r Runner, info Info) *Device {
	return &Device{runner: r, info: info, max: map[VCP]int{}}
}

// Info implements Display.
func (d *Device) Info() Info { return d.info }

// Caps implements Display.
func (d *Device) Caps() Caps {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.caps
}

func (d *Device) bus() string { return strconv.Itoa(d.info.Bus) }

// Get reads the current and maximum value of a continuous VCP (read-only).
func (d *Device) Get(ctx context.Context, vcp VCP) (int, int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.get(ctx, vcp)
}

func (d *Device) get(ctx context.Context, vcp VCP) (int, int, error) {
	out, err := d.runner.Run(ctx, "getvcp", vcp.hex(), "--bus", d.bus(), "--brief")
	if err != nil {
		return 0, 0, fmt.Errorf("getvcp %s on bus %d: %w", vcp, d.info.Bus, err)
	}
	cur, max, err := parseGetVCP(out)
	if err != nil {
		return 0, 0, fmt.Errorf("getvcp %s on bus %d: %w", vcp, d.info.Bus, err)
	}
	return cur, max, nil
}

// Probe reads the four whitelisted VCPs and fills in the capabilities.
// A VCP that does not respond only disables the matching capability.
func (d *Device) Probe(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var errs []error
	d.caps = Caps{}
	d.max = map[VCP]int{}
	for _, vcp := range probed {
		_, max, err := d.get(ctx, vcp)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		d.max[vcp] = max
	}
	if m, ok := d.max[VCPBrightness]; ok {
		d.caps.Brightness, d.caps.BrightnessMax = true, m
	}
	r, rok := d.max[VCPRedGain]
	g, gok := d.max[VCPGreenGain]
	b, bok := d.max[VCPBlueGain]
	if rok && gok && bok {
		d.caps.RGB, d.caps.RGBMax = true, [3]int{r, g, b}
	}
	return errors.Join(errs...)
}

// Set writes v to a whitelisted VCP, clamped to the monitor's [0, max].
func (d *Device) Set(ctx context.Context, vcp VCP, v int) error {
	if !vcp.Allowed() {
		return fmt.Errorf("%w: %s", ErrVCPNotAllowed, vcp)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	max, ok := d.max[vcp]
	if !ok {
		return fmt.Errorf("%w: %s on bus %d", ErrUnsupported, vcp, d.info.Bus)
	}
	if v < 0 {
		v = 0
	}
	if v > max {
		v = max
	}
	if _, err := d.runner.Run(ctx, "setvcp", vcp.hex(), strconv.Itoa(v), "--bus", d.bus()); err != nil {
		return fmt.Errorf("setvcp %s=%d on bus %d: %w", vcp, v, d.info.Bus, err)
	}
	return nil
}

var _ Display = (*Device)(nil)

func trimLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		out = append(out, strings.TrimRight(l, "\r"))
	}
	return out
}
