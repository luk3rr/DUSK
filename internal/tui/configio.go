// Filename: internal/tui/configio.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"context"
	"time"

	"dusk/internal/config"
	"dusk/internal/gamma"
)

// ConfigStore reads and writes the configuration file.
type ConfigStore interface {
	// Path is the file shown to the user.
	Path() string
	// Load reads the file; a missing file returns an error that satisfies os.IsNotExist.
	Load() (*config.Config, error)
	// Save validates and writes the file (with a backup of the old one).
	Save(cfg *config.Config) error
}

// FileConfig is the ConfigStore backed by a TOML file.
type FileConfig struct {
	File string
	Now  func() time.Time
}

// Path implements ConfigStore.
func (f FileConfig) Path() string { return f.File }

// Load implements ConfigStore.
func (f FileConfig) Load() (*config.Config, error) { return config.Load(f.File) }

// Save implements ConfigStore.
func (f FileConfig) Save(cfg *config.Config) error {
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	_, err := config.Write(f.File, cfg, now())
	return err
}

// ScreenSource lists the screens that accept software color.
type ScreenSource interface {
	Screens(ctx context.Context) ([]gamma.Output, error)
}

// GammaScreens adapts a gamma.Backend to ScreenSource.
type GammaScreens struct{ Backend gamma.Backend }

// Screens implements ScreenSource.
func (g GammaScreens) Screens(ctx context.Context) ([]gamma.Output, error) {
	if g.Backend == nil {
		return nil, gamma.ErrNoBackend
	}
	return g.Backend.Outputs(ctx)
}
