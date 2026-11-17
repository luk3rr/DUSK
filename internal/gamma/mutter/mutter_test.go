// Filename: internal/gamma/mutter/mutter_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package mutter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"dusk/internal/gamma"
)

type fakeConn struct {
	serial  uint32
	calls   []string
	setArgs [][]any
	failSet func(serial uint32) error
	sig     chan struct{}
	err     error
}

func variant(s string) dbus.Variant { return dbus.MakeVariant(s) }

func (f *fakeConn) Call(_ context.Context, method string, args ...any) ([]any, error) {
	f.calls = append(f.calls, method)
	if f.err != nil {
		return nil, f.err
	}
	switch method {
	case "GetResources":
		return []any{
			f.serial,
			[]crtcInfo{{ID: 0}, {ID: 1}, {ID: 2}},
			[]outputInfo{
				{ID: 10, CurrentCRTC: 0, Name: "DP-1", Props: map[string]dbus.Variant{"vendor": variant("AOC"), "product": variant("24G4"), "serial": variant("12VP9XA002026")}},
				{ID: 11, CurrentCRTC: 1, Name: "HDMI-A-1", Props: map[string]dbus.Variant{"vendor": variant("GSM"), "product": variant("LG ULTRAWIDE"), "serial": variant("0x01010101")}},
				{ID: 12, CurrentCRTC: -1, Name: "DP-2", Props: map[string]dbus.Variant{}},
			},
			[]modeInfo{},
			int32(16384), int32(16384),
		}, nil
	case "GetCrtcGamma":
		r := gamma.Identity(4)
		return []any{r.R, r.G, r.B}, nil
	case "SetCrtcGamma":
		f.setArgs = append(f.setArgs, args)
		if f.failSet != nil {
			if err := f.failSet(args[0].(uint32)); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	return nil, errors.New("unexpected method " + method)
}

func (f *fakeConn) Signals(context.Context) (<-chan struct{}, error) { return f.sig, nil }

func TestOutputs(t *testing.T) {
	b := New(&fakeConn{serial: 7})
	outs, err := b.Outputs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 2 {
		t.Fatalf("outputs = %+v", outs)
	}
	if outs[0].Name != "DP-1" || outs[0].Key() != "24G4#12VP9XA002026" || outs[0].CRTC != 0 {
		t.Errorf("out0 = %+v", outs[0])
	}
	if outs[1].Serial != "" || outs[1].Key() != "LG ULTRAWIDE" || outs[1].CRTC != 1 {
		t.Errorf("out1 = %+v", outs[1])
	}
}

func TestSetUsesSerialAndCRTC(t *testing.T) {
	f := &fakeConn{serial: 7}
	b := New(f)
	o := gamma.Output{CRTC: 1}
	if err := b.Set(context.Background(), o, gamma.Identity(4)); err != nil {
		t.Fatal(err)
	}
	if len(f.setArgs) != 1 || f.setArgs[0][0].(uint32) != 7 || f.setArgs[0][1].(uint32) != 1 {
		t.Fatalf("args = %+v", f.setArgs)
	}
}

func TestSetRejectsInvalidRampWithoutCalling(t *testing.T) {
	f := &fakeConn{serial: 7}
	b := New(f)
	err := b.Set(context.Background(), gamma.Output{}, gamma.Ramp{R: []uint16{9, 1}, G: []uint16{0, 1}, B: []uint16{0, 1}})
	if !errors.Is(err, gamma.ErrInvalidRamp) {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("D-Bus was called: %v", f.calls)
	}
}

func TestStaleSerialRetriedOnce(t *testing.T) {
	f := &fakeConn{serial: 7}
	b := New(f)
	if _, err := b.Outputs(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.serial = 8 // configuration changed behind our back
	f.failSet = func(serial uint32) error {
		if serial != 8 {
			return errors.New("invalid serial")
		}
		return nil
	}
	if err := b.Set(context.Background(), gamma.Output{CRTC: 0}, gamma.Identity(4)); err != nil {
		t.Fatal(err)
	}
	if n := len(f.setArgs); n != 2 || f.setArgs[1][0].(uint32) != 8 {
		t.Fatalf("setArgs = %+v", f.setArgs)
	}
}

func TestSetErrorWithSameSerialIsReturned(t *testing.T) {
	f := &fakeConn{serial: 7, failSet: func(uint32) error { return errors.New("denied") }}
	b := New(f)
	if err := b.Set(context.Background(), gamma.Output{}, gamma.Identity(4)); err == nil {
		t.Fatal("want error")
	}
	if len(f.setArgs) != 1 {
		t.Fatalf("retried without a serial change: %d calls", len(f.setArgs))
	}
}

func TestGet(t *testing.T) {
	b := New(&fakeConn{serial: 7})
	r, err := b.Get(context.Background(), gamma.Output{CRTC: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Equal(gamma.Identity(4)) {
		t.Fatalf("ramp = %+v", r)
	}
}

func TestNoBackend(t *testing.T) {
	b := New(&fakeConn{err: dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"}})
	_, err := b.Outputs(context.Background())
	if !errors.Is(err, gamma.ErrNoBackend) {
		t.Fatalf("err = %v", err)
	}
}

func TestWatchForwardsAndStops(t *testing.T) {
	f := &fakeConn{serial: 7, sig: make(chan struct{}, 1)}
	b := New(f)
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- b.Watch(ctx, ch) }()
	f.sig <- struct{}{}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("event not forwarded")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Watch did not stop")
	}
}
