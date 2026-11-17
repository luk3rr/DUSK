// Filename: internal/gamma/lazy_test.go
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

func TestLazyRetriesUntilConnected(t *testing.T) {
	fake := gammatest.New(gamma.Output{CRTC: 0, Name: "DP-1"})
	calls := 0
	b := gamma.Lazy(func(context.Context) (gamma.Backend, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("desktop not ready")
		}
		return fake, nil
	})
	ctx := context.Background()
	if _, err := b.Get(ctx, gamma.Output{}); !errors.Is(err, gamma.ErrNoBackend) {
		t.Fatalf("Get before connect: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := b.Outputs(ctx); err == nil {
			t.Fatal("want error while not ready")
		}
	}
	outs, err := b.Outputs(ctx)
	if err != nil || len(outs) != 1 {
		t.Fatalf("outs=%v err=%v", outs, err)
	}
	if err := b.Set(ctx, outs[0], gamma.Identity(gammatest.Size)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Outputs(ctx); err != nil || calls != 3 {
		t.Fatalf("must not reconnect once connected: calls=%d err=%v", calls, err)
	}
}
