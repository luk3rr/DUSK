// Filename: internal/service/service.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

// Package service controls the dusk systemd user service for the CLI and the TUI.
package service

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dusk/internal/ddc"
	"dusk/internal/install"
)

// Manager talks to systemctl --user.
type Manager struct {
	Systemctl ddc.Runner // runs systemctl
	UnitPath  string     // where the unit file lives, to tell whether it is installed
}

// State is what the service looks like right now.
type State struct {
	Installed bool
	Active    bool
}

// State reads whether the unit file exists and whether the service is active.
func (m Manager) State(ctx context.Context) State {
	var s State
	if _, err := os.Stat(m.UnitPath); err == nil {
		s.Installed = true
	}
	// is-active exits non-zero when the service is not active.
	out, err := m.Systemctl.Run(ctx, "--user", "is-active", install.UnitName)
	s.Active = err == nil && strings.TrimSpace(out) == "active"
	return s
}

// Start starts the installed service.
func (m Manager) Start(ctx context.Context) error {
	if _, err := m.Systemctl.Run(ctx, "--user", "start", install.UnitName); err != nil {
		return fmt.Errorf("starting %s: %w", install.UnitName, err)
	}
	return nil
}

// Stop stops the service (the daemon restores the normal colors on its way out).
func (m Manager) Stop(ctx context.Context) error {
	if _, err := m.Systemctl.Run(ctx, "--user", "stop", install.UnitName); err != nil {
		return fmt.Errorf("stopping %s: %w", install.UnitName, err)
	}
	return nil
}
