// Filename: cmd/dusk/tui.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/x/term"

	"dusk/internal/ddc"
	"dusk/internal/gamma"
	"dusk/internal/ipc"
	"dusk/internal/tui"
)

// Seams that tests replace so they never need a terminal.
var (
	interactive = func() bool {
		return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
	}
	tuiRun = tui.Run
)

// runTUI implements `dusk` without arguments in a terminal.
func runTUI(ctx context.Context, r ddc.Runner, stderr io.Writer) int {
	sock, err := ipc.SocketPath()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	cfgPath, err := defaultConfigPath()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	deps := tui.Deps{
		Client:  tui.IPCClient{Socket: sock},
		Config:  tui.FileConfig{File: cfgPath},
		Screens: tui.GammaScreens{Backend: gamma.Lazy(gammaConnect)},
		Service: cliService{configPath: cfgPath},
		DDC:     cliReset{runner: r},
	}
	if err := tuiRun(ctx, deps); err != nil {
		fmt.Fprintln(stderr, "dusk:", err)
		return exitUsage
	}
	return exitOK
}
