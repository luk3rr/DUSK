// Filename: internal/gamma/gammatest/gammatest.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

// Package gammatest provides an in-memory gamma.Backend for tests.
package gammatest

import (
	"context"
	"sync"

	"dusk/internal/gamma"
)

// Size is the ramp size of the fake outputs.
const Size = 1024

// Fake is an in-memory backend. Set FailSet/FailGet to inject errors per CRTC.
type Fake struct {
	Outs    []gamma.Output
	FailSet map[uint32]error
	FailGet map[uint32]error
	OutsErr error

	mu     sync.Mutex
	ramps  map[uint32]gamma.Ramp
	hist   map[uint32][]gamma.Ramp
	sets   int
	notify chan struct{}
}

// New creates a Fake whose outputs start with the identity ramp.
func New(outs ...gamma.Output) *Fake {
	f := &Fake{
		Outs:    outs,
		FailSet: map[uint32]error{},
		FailGet: map[uint32]error{},
		ramps:   map[uint32]gamma.Ramp{},
		hist:    map[uint32][]gamma.Ramp{},
		notify:  make(chan struct{}, 1),
	}
	for _, o := range outs {
		f.ramps[o.CRTC] = gamma.Identity(Size)
	}
	return f
}

// Outputs implements gamma.Backend.
func (f *Fake) Outputs(context.Context) ([]gamma.Output, error) {
	if f.OutsErr != nil {
		return nil, f.OutsErr
	}
	return append([]gamma.Output(nil), f.Outs...), nil
}

// Get implements gamma.Backend.
func (f *Fake) Get(_ context.Context, o gamma.Output) (gamma.Ramp, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.FailGet[o.CRTC]; err != nil {
		return gamma.Ramp{}, err
	}
	r, ok := f.ramps[o.CRTC]
	if !ok {
		return gamma.Ramp{}, gamma.ErrOutputGone
	}
	return r, nil
}

// Set implements gamma.Backend. It rejects invalid ramps like the real one.
func (f *Fake) Set(_ context.Context, o gamma.Output, r gamma.Ramp) error {
	if err := r.Valid(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.FailSet[o.CRTC]; err != nil {
		return err
	}
	if _, ok := f.ramps[o.CRTC]; !ok {
		return gamma.ErrOutputGone
	}
	f.ramps[o.CRTC] = r
	f.hist[o.CRTC] = append(f.hist[o.CRTC], r)
	f.sets++
	return nil
}

// Sets returns how many successful Set calls happened.
func (f *Fake) Sets() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sets
}

// History returns every ramp written to a CRTC, oldest first.
func (f *Fake) History(crtc uint32) []gamma.Ramp {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gamma.Ramp(nil), f.hist[crtc]...)
}

// Overwrite changes a ramp behind the controller's back (simulates gsd-color).
func (f *Fake) Overwrite(o gamma.Output, r gamma.Ramp) {
	f.mu.Lock()
	f.ramps[o.CRTC] = r
	f.mu.Unlock()
}

// Watch implements gamma.Backend: it forwards Notify calls until ctx is done.
func (f *Fake) Watch(ctx context.Context, ch chan<- struct{}) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-f.notify:
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// Notify simulates a monitors-changed event.
func (f *Fake) Notify() {
	select {
	case f.notify <- struct{}{}:
	default:
	}
}

var _ gamma.Backend = (*Fake)(nil)
