// Filename: internal/gamma/lazy.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package gamma

import (
	"context"
	"sync"
	"time"
)

const lazyRetry = 10 * time.Second

type lazy struct {
	connect func(ctx context.Context) (Backend, error)

	mu sync.Mutex
	b  Backend
}

// Lazy returns a Backend that connects on first use and keeps retrying while
// the desktop is not ready (for example right after login).
func Lazy(connect func(ctx context.Context) (Backend, error)) Backend {
	return &lazy{connect: connect}
}

func (l *lazy) get(ctx context.Context) (Backend, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.b != nil {
		return l.b, nil
	}
	b, err := l.connect(ctx)
	if err != nil {
		return nil, err
	}
	l.b = b
	return b, nil
}

func (l *lazy) ready() Backend {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b
}

// Outputs implements Backend; it is the call that (re)tries the connection.
func (l *lazy) Outputs(ctx context.Context) ([]Output, error) {
	b, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return b.Outputs(ctx)
}

// Get implements Backend.
func (l *lazy) Get(ctx context.Context, o Output) (Ramp, error) {
	b := l.ready()
	if b == nil {
		return Ramp{}, ErrNoBackend
	}
	return b.Get(ctx, o)
}

// Set implements Backend.
func (l *lazy) Set(ctx context.Context, o Output, r Ramp) error {
	b := l.ready()
	if b == nil {
		return ErrNoBackend
	}
	return b.Set(ctx, o, r)
}

// Watch implements Backend: it waits for the connection, then delegates.
func (l *lazy) Watch(ctx context.Context, ch chan<- struct{}) error {
	for {
		if b, err := l.get(ctx); err == nil {
			return b.Watch(ctx, ch)
		}
		t := time.NewTimer(lazyRetry)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}
