// Filename: cmd/dusk/gamma.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"dusk/internal/gamma"
	"dusk/internal/gamma/mutter"
)

// gammaConnect is the seam that tests replace so they never touch D-Bus.
var gammaConnect = func(ctx context.Context) (gamma.Backend, error) {
	return mutter.Connect(ctx)
}

// runResetGamma implements `dusk reset-gamma`: it applies the identity ramp to
// every output. It is idempotent and keeps going after a failure on one output.
func runResetGamma(ctx context.Context, stdout, stderr io.Writer) int {
	b, err := gammaConnect(ctx)
	if err != nil {
		if errors.Is(err, gamma.ErrNoBackend) {
			fmt.Fprintf(stderr, "cannot reset colors: %v\n", err)
		} else {
			fmt.Fprintln(stderr, err)
		}
		return exitUsage
	}
	outs, err := b.Outputs(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	failed := false
	for _, o := range outs {
		cur, err := b.Get(ctx, o)
		if err == nil {
			err = b.Set(ctx, o, gamma.Identity(cur.Len()))
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", o.Name, err)
			failed = true
			continue
		}
		fmt.Fprintf(stdout, "%s: reset\n", o.Name)
	}
	if failed {
		return exitUsage
	}
	return exitOK
}
