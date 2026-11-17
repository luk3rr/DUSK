// Filename: cmd/dusk/tuiadapters.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dusk/internal/ddc"
	"dusk/internal/install"
	"dusk/internal/legacy"
	"dusk/internal/logs"
	"dusk/internal/monitors"
	"dusk/internal/service"
	"dusk/internal/state"
	"dusk/internal/tui"
)

// cliService implements tui.Service with the same seams the CLI commands use.
type cliService struct{ configPath string }

func (c cliService) unitDir() (string, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "systemd", "user"), nil
}

func (c cliService) manager() service.Manager {
	dir, _ := c.unitDir()
	return service.Manager{Systemctl: systemctl, UnitPath: filepath.Join(dir, install.UnitName)}
}

func (c cliService) options(out io.Writer) (install.Options, error) {
	dir, err := c.unitDir()
	if err != nil {
		return install.Options{}, err
	}
	def, _ := defaultConfigPath()
	return install.Options{
		ConfigPath:     c.configPath,
		ExplicitConfig: c.configPath != def,
		UnitDir:        dir,
		Systemctl:      systemctl,
		Yes:            true,
		In:             strings.NewReader(""),
		Out:            out,
	}, nil
}

func (c cliService) State(ctx context.Context) service.State { return c.manager().State(ctx) }
func (c cliService) Start(ctx context.Context) error         { return c.manager().Start(ctx) }
func (c cliService) Stop(ctx context.Context) error          { return c.manager().Stop(ctx) }

func (c cliService) Install(ctx context.Context) (string, error) {
	var out bytes.Buffer
	o, err := c.options(&out)
	if err != nil {
		return "", err
	}
	exe, _ := executable()
	if o.Bin, err = install.ResolveBin(exe, ""); err != nil {
		return "", err
	}
	err = install.Install(ctx, o)
	return lastLines(out.String(), 3), err
}

func (c cliService) Uninstall(ctx context.Context) (string, error) {
	var out bytes.Buffer
	o, err := c.options(&out)
	if err != nil {
		return "", err
	}
	keep := []string{o.ConfigPath}
	if sp, err := state.DefaultPath(); err == nil {
		keep = append(keep, sp)
	}
	err = install.Uninstall(ctx, o, keep)
	return lastLines(out.String(), 3), err
}

func (c cliService) Logs(ctx context.Context, n int) ([]string, error) {
	var out bytes.Buffer
	if err := logs.Show(ctx, journalctl, logs.Options{Lines: n}, &out, io.Discard); err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(out.String(), "\n"), "\n"), nil
}

// lastLines keeps the last n non-empty lines of s.
func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		// The CLI closes with command tips that mean nothing inside the TUI.
		if t == "" || strings.HasPrefix(t, "Check it:") || strings.HasPrefix(t, "Logs:") || strings.HasPrefix(t, "Pause it:") || strings.HasPrefix(t, "Remove it:") {
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// cliReset implements tui.DDCReset on top of the legacy package.
type cliReset struct{ runner ddc.Runner }

func (c cliReset) plan(ctx context.Context, toMax bool) ([]legacy.Item, state.Store, error) {
	displays, err := monitors.Discover(ctx, c.runner)
	if err != nil {
		return nil, state.Store{}, err
	}
	sp, err := state.DefaultPath()
	if err != nil {
		return nil, state.Store{}, err
	}
	store := state.Store{Path: sp}
	st, err := store.Load()
	if err != nil {
		st = state.New() // unreadable saved originals are ignored, like `dusk reset-ddc` does
	}
	items, err := legacy.Plan(ctx, displays, st, toMax)
	return items, store, err
}

// Plan implements tui.DDCReset.
func (c cliReset) Plan(ctx context.Context, toMax bool) ([]tui.ResetItem, error) {
	items, _, err := c.plan(ctx, toMax)
	out := make([]tui.ResetItem, 0, len(items))
	for _, it := range items {
		out = append(out, tui.ResetItem{
			Key: it.Key, Current: it.Current, Target: it.Target,
			FromState: it.FromState, NoOriginal: it.NoOriginal, Normal: it.Normal(),
		})
	}
	return out, err
}

// Apply implements tui.DDCReset.
func (c cliReset) Apply(ctx context.Context, key string, toMax bool) error {
	items, store, err := c.plan(ctx, toMax)
	for _, it := range items {
		if it.Key != key {
			continue
		}
		if err := legacy.Apply(ctx, it); err != nil {
			return err
		}
		return legacy.Forget(store, it.Key)
	}
	if err != nil {
		return err
	}
	return os.ErrNotExist
}
