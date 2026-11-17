// Filename: internal/gamma/mutter/mutter.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

// Package mutter implements gamma.Backend on top of GNOME's Mutter
// (org.gnome.Mutter.DisplayConfig GetCrtcGamma/SetCrtcGamma). All D-Bus
// traffic goes through Conn, so tests never need a session bus.
package mutter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"dusk/internal/gamma"
)

const callTimeout = 5 * time.Second

// Conn is the small slice of D-Bus that the backend needs.
type Conn interface {
	// Call invokes a DisplayConfig method and returns the reply body.
	Call(ctx context.Context, method string, args ...any) ([]any, error)
	// Signals delivers an event for every MonitorsChanged signal until ctx is done.
	Signals(ctx context.Context) (<-chan struct{}, error)
}

type crtcInfo struct {
	ID         uint32
	Winsys     int64
	X, Y, W, H int32
	Mode       int32
	Transform  uint32
	Transforms []uint32
	Props      map[string]dbus.Variant
}

type outputInfo struct {
	ID          uint32
	Winsys      int64
	CurrentCRTC int32
	Possible    []uint32
	Name        string
	Modes       []uint32
	Clones      []uint32
	Props       map[string]dbus.Variant
}

type modeInfo struct {
	ID     uint32
	Winsys int64
	W, H   uint32
	Freq   float64
	Flags  uint32
}

// Backend is the Mutter gamma backend.
type Backend struct {
	conn Conn

	mu     sync.Mutex
	serial uint32
}

var _ gamma.Backend = (*Backend)(nil)

// New wraps a Conn.
func New(c Conn) *Backend { return &Backend{conn: c} }

// Connect opens the session bus and checks that Mutter's DisplayConfig answers.
// It returns gamma.ErrNoBackend (wrapped with the reason) when it does not.
func Connect(ctx context.Context) (*Backend, error) {
	bus, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("%w: session bus: %v", gamma.ErrNoBackend, err)
	}
	b := New(&busConn{bus: bus})
	if _, err := b.Outputs(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

func noService(err error) bool {
	var de dbus.Error
	if !errors.As(err, &de) {
		return false
	}
	switch de.Name {
	case "org.freedesktop.DBus.Error.ServiceUnknown", "org.freedesktop.DBus.Error.NameHasNoOwner", "org.freedesktop.DBus.Error.UnknownMethod", "org.freedesktop.DBus.Error.NoReply":
		return true
	}
	return false
}

func (b *Backend) call(ctx context.Context, method string, args ...any) ([]any, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	body, err := b.conn.Call(ctx, method, args...)
	if err != nil {
		if noService(err) {
			return nil, fmt.Errorf("%w: Mutter DisplayConfig unavailable: %v", gamma.ErrNoBackend, err)
		}
		return nil, fmt.Errorf("mutter %s: %w", method, err)
	}
	return body, nil
}

func prop(m map[string]dbus.Variant, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.Value().(string)
	return s
}

// cleanSerial treats the placeholder serials some monitors report as "no serial".
func cleanSerial(s string) string {
	s = strings.TrimSpace(s)
	switch s {
	case "", "0x01010101", "0x00000000", "0":
		return ""
	}
	return s
}

// Outputs implements gamma.Backend.
func (b *Backend) Outputs(ctx context.Context) ([]gamma.Output, error) {
	body, err := b.call(ctx, "GetResources")
	if err != nil {
		return nil, err
	}
	var (
		serial  uint32
		crtcs   []crtcInfo
		outputs []outputInfo
		modes   []modeInfo
		maxW    int32
		maxH    int32
	)
	if err := dbus.Store(body, &serial, &crtcs, &outputs, &modes, &maxW, &maxH); err != nil {
		return nil, fmt.Errorf("mutter GetResources: unexpected reply: %w", err)
	}
	b.mu.Lock()
	b.serial = serial
	b.mu.Unlock()
	var out []gamma.Output
	for _, o := range outputs {
		if o.CurrentCRTC < 0 {
			continue
		}
		out = append(out, gamma.Output{
			CRTC:    uint32(o.CurrentCRTC),
			Name:    o.Name,
			Vendor:  prop(o.Props, "vendor"),
			Product: prop(o.Props, "product"),
			Serial:  cleanSerial(prop(o.Props, "serial")),
		})
	}
	return out, nil
}

func (b *Backend) currentSerial() uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.serial
}

// withSerial runs fn with the cached serial; if it fails and the serial has
// changed since, it refreshes and retries once.
func (b *Backend) withSerial(ctx context.Context, fn func(serial uint32) error) error {
	if b.currentSerial() == 0 {
		if _, err := b.Outputs(ctx); err != nil {
			return err
		}
	}
	old := b.currentSerial()
	err := fn(old)
	if err == nil {
		return nil
	}
	if _, rerr := b.Outputs(ctx); rerr != nil {
		return err
	}
	if b.currentSerial() != old {
		return fn(b.currentSerial())
	}
	return err
}

// Get implements gamma.Backend.
func (b *Backend) Get(ctx context.Context, o gamma.Output) (gamma.Ramp, error) {
	var r gamma.Ramp
	err := b.withSerial(ctx, func(serial uint32) error {
		body, err := b.call(ctx, "GetCrtcGamma", serial, o.CRTC)
		if err != nil {
			return err
		}
		return dbus.Store(body, &r.R, &r.G, &r.B)
	})
	if err != nil {
		return gamma.Ramp{}, err
	}
	return r, nil
}

// Set implements gamma.Backend. An invalid ramp is rejected before any D-Bus call.
func (b *Backend) Set(ctx context.Context, o gamma.Output, r gamma.Ramp) error {
	if err := r.Valid(); err != nil {
		return err
	}
	return b.withSerial(ctx, func(serial uint32) error {
		_, err := b.call(ctx, "SetCrtcGamma", serial, o.CRTC, r.R, r.G, r.B)
		return err
	})
}

// Watch implements gamma.Backend.
func (b *Backend) Watch(ctx context.Context, ch chan<- struct{}) error {
	sig, err := b.conn.Signals(ctx)
	if err != nil {
		return fmt.Errorf("mutter: subscribing to MonitorsChanged: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-sig:
			if !ok {
				return nil
			}
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

const (
	busName = "org.gnome.Mutter.DisplayConfig"
	objPath = dbus.ObjectPath("/org/gnome/Mutter/DisplayConfig")
	ifaceNm = "org.gnome.Mutter.DisplayConfig"
)

// busConn is the real Conn over the session bus.
type busConn struct{ bus *dbus.Conn }

func (c *busConn) Call(ctx context.Context, method string, args ...any) ([]any, error) {
	call := c.bus.Object(busName, objPath).CallWithContext(ctx, ifaceNm+"."+method, 0, args...)
	if call.Err != nil {
		return nil, call.Err
	}
	return call.Body, nil
}

func (c *busConn) Signals(ctx context.Context) (<-chan struct{}, error) {
	if err := c.bus.AddMatchSignalContext(ctx,
		dbus.WithMatchObjectPath(objPath),
		dbus.WithMatchInterface(ifaceNm),
		dbus.WithMatchMember("MonitorsChanged")); err != nil {
		return nil, err
	}
	raw := make(chan *dbus.Signal, 8)
	c.bus.Signal(raw)
	out := make(chan struct{}, 1)
	go func() {
		defer c.bus.RemoveSignal(raw)
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case s, ok := <-raw:
				if !ok {
					return
				}
				if s.Name != ifaceNm+".MonitorsChanged" {
					continue
				}
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out, nil
}
