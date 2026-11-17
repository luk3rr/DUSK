// Filename: internal/gamma/gamma_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package gamma_test

import (
	"context"
	"errors"
	"testing"

	"dusk/internal/gamma"
	"dusk/internal/gamma/gammatest"
)

func TestIdentity(t *testing.T) {
	r := gamma.Identity(1024)
	if r.Len() != 1024 || r.R[0] != 0 || r.R[1023] != 65535 {
		t.Fatalf("bad identity: len=%d first=%d last=%d", r.Len(), r.R[0], r.R[1023])
	}
	if err := r.Valid(); err != nil {
		t.Fatal(err)
	}
	if !r.Equal(gamma.Build(1024, 1, 1, 1, 1)) {
		t.Fatal("identity differs from Build(1,1,1,1)")
	}
}

func TestBuild(t *testing.T) {
	cases := []struct {
		name         string
		r, g, b, s   float64
		wantR, wantG uint16
		wantB        uint16
	}{
		{"full", 1, 1, 1, 1, 65535, 65535, 65535},
		{"blue 30%", 1, 1, 0.3, 1, 65535, 65535, 19661},
		{"half scale", 1, 1, 1, 0.5, 32768, 32768, 32768},
		{"clamp high", 5, 5, 5, 9, 65535, 65535, 65535},
		{"clamp low", -1, -1, -1, 1, 0, 0, 0},
		{"zero scale", 1, 1, 1, 0, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := gamma.Build(1024, c.r, c.g, c.b, c.s)
			if err := r.Valid(); err != nil {
				t.Fatal(err)
			}
			if r.R[1023] != c.wantR || r.G[1023] != c.wantG || r.B[1023] != c.wantB {
				t.Fatalf("last = %d/%d/%d, want %d/%d/%d", r.R[1023], r.G[1023], r.B[1023], c.wantR, c.wantG, c.wantB)
			}
		})
	}
	if gamma.Build(0, 1, 1, 1, 1).Len() != 2 {
		t.Fatal("n < 2 must become 2")
	}
}

func TestBrightnessScale(t *testing.T) {
	cases := []struct {
		p, floor int
		want     float64
	}{
		{0, 10, 0.10}, {100, 10, 1}, {50, 10, 0.55}, {0, 0, 0}, {-5, 10, 0.10}, {200, 10, 1}, {30, 100, 1},
	}
	for _, c := range cases {
		if got := gamma.BrightnessScale(c.p, c.floor); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("BrightnessScale(%d,%d) = %v, want %v", c.p, c.floor, got, c.want)
		}
	}
}

func TestValidRejects(t *testing.T) {
	bad := []gamma.Ramp{
		{},
		{R: []uint16{0, 1}, G: []uint16{0}, B: []uint16{0, 1}},
		{R: []uint16{5, 1}, G: []uint16{0, 1}, B: []uint16{0, 1}},
	}
	for i, r := range bad {
		if err := r.Valid(); !errors.Is(err, gamma.ErrInvalidRamp) {
			t.Errorf("case %d: err = %v", i, err)
		}
	}
}

func TestReset(t *testing.T) {
	f := gammatest.New(
		gamma.Output{CRTC: 0, Name: "DP-1", Product: "A"},
		gamma.Output{CRTC: 1, Name: "DP-2", Product: "B"},
	)
	for _, o := range f.Outs {
		if err := f.Set(context.Background(), o, gamma.Build(1024, 1, 1, 0.3, 0.5)); err != nil {
			t.Fatal(err)
		}
	}
	if err := gamma.Reset(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	for _, o := range f.Outs {
		r, _ := f.Get(context.Background(), o)
		if !r.Equal(gamma.Identity(1024)) {
			t.Fatalf("%s not reset", o.Name)
		}
	}
}

func TestResetContinuesAfterFailure(t *testing.T) {
	f := gammatest.New(
		gamma.Output{CRTC: 0, Name: "DP-1"},
		gamma.Output{CRTC: 1, Name: "DP-2"},
	)
	f.FailSet[0] = errors.New("boom")
	_ = f.Set(context.Background(), f.Outs[1], gamma.Build(1024, 1, 1, 0.3, 1))
	err := gamma.Reset(context.Background(), f)
	if err == nil {
		t.Fatal("want an error")
	}
	r, _ := f.Get(context.Background(), f.Outs[1])
	if !r.Equal(gamma.Identity(1024)) {
		t.Fatal("second output was not reset")
	}
}
