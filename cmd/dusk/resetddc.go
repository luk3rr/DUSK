// Filename: cmd/dusk/resetddc.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"dusk/internal/ddc"
	"dusk/internal/legacy"
	"dusk/internal/monitors"
	"dusk/internal/state"
)

// runResetDDC implements `dusk reset-ddc`: it restores the RGB gains that older
// dusk versions wrote into the monitors. It asks before every monitor unless --yes.
func runResetDDC(ctx context.Context, args []string, r ddc.Runner, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reset-ddc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	yes := fs.Bool("yes", false, "restore every monitor without asking")
	toMax := fs.Bool("to-max", false, "when no original was saved, set the gains to the monitor's maximum (100%)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	displays, err := monitors.Discover(ctx, r)
	if err != nil {
		fmt.Fprintf(stderr, "dusk reset-ddc: %v\n", err)
		return exitUsage
	}
	sp, err := state.DefaultPath()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	store := state.Store{Path: sp}
	st, err := store.Load()
	if err != nil {
		fmt.Fprintf(stderr, "warning: ignoring the saved originals: %v\n", err)
		st = state.New()
	}
	items, planErr := legacy.Plan(ctx, displays, st, *toMax)
	if planErr != nil {
		fmt.Fprintf(stderr, "warning: some monitors could not be read:\n%v\n", planErr)
	}
	if len(items) == 0 {
		fmt.Fprintln(stdout, "no monitor with DDC color control found; nothing to restore")
		if planErr != nil {
			return exitUsage
		}
		return exitOK
	}
	in := bufio.NewReader(stdin)
	failed := planErr != nil
	for _, it := range items {
		fmt.Fprintf(stdout, "%s: R %d  G %d  B %d", it.Key, it.Current[0], it.Current[1], it.Current[2])
		if it.NoOriginal {
			fmt.Fprintln(stdout, "  (no original saved by an older dusk: left alone; if this is not your normal setting, run: dusk reset-ddc --to-max)")
			continue
		}
		if it.Normal() {
			fmt.Fprintln(stdout, "  (already normal)")
			continue
		}
		src := "the monitor's maximum"
		if it.FromState {
			src = "the value saved by an older dusk"
		}
		fmt.Fprintf(stdout, "  ->  R %d  G %d  B %d (%s)\n", it.Target[0], it.Target[1], it.Target[2], src)
		if !*yes && !confirm(in, stdout, fmt.Sprintf("Restore %s? [y/N] ", it.Key)) {
			fmt.Fprintln(stdout, "  skipped")
			continue
		}
		if err := legacy.Apply(ctx, it); err != nil {
			fmt.Fprintf(stderr, "  failed: %v\n", err)
			failed = true
			continue
		}
		fmt.Fprintln(stdout, "  restored")
		if err := legacy.Forget(store, it.Key); err != nil {
			fmt.Fprintf(stderr, "  warning: could not update the saved originals: %v\n", err)
		}
	}
	if failed {
		return exitUsage
	}
	return exitOK
}

// confirm asks a yes/no question; anything but "y"/"yes" (including end of input) is no.
func confirm(in *bufio.Reader, out io.Writer, question string) bool {
	fmt.Fprint(out, question)
	line, _ := in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
