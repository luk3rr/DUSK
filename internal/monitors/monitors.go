// Package monitors discovers monitors and matches them to the configuration.
package monitors

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"dusk/internal/config"
	"dusk/internal/ddc"
)

// Discover runs `ddcutil detect` and probes every monitor (read-only).
// A probe failure on one monitor does not stop the others: it is returned
// with empty capabilities.
func Discover(ctx context.Context, r ddc.Runner) ([]ddc.Display, error) {
	out, err := r.Run(ctx, "detect", "--brief")
	if err != nil {
		return nil, fmt.Errorf("detecting monitors: %w", err)
	}
	infos := ddc.ParseDetect(out)
	sort.Slice(infos, func(i, j int) bool { return infos[i].Bus < infos[j].Bus })
	displays := make([]ddc.Display, 0, len(infos))
	for _, info := range infos {
		d := ddc.NewDevice(r, info)
		_ = d.Probe(ctx) // partial errors just leave the matching capabilities off
		displays = append(displays, d)
	}
	return displays, nil
}

// Assignment ties a detected monitor to its entry in the configuration.
// Monitor is nil when there is no entry (no schedule).
type Assignment struct {
	Display ddc.Display
	Monitor *config.Monitor
}

// Match assigns each display to a configuration entry. "model#serial" wins
// over "model"; comparison is case-insensitive.
func Match(cfg *config.Config, displays []ddc.Display) []Assignment {
	byMatch := map[string]*config.Monitor{}
	if cfg != nil {
		for i := range cfg.Monitors {
			byMatch[strings.ToLower(cfg.Monitors[i].Match)] = &cfg.Monitors[i]
		}
	}
	out := make([]Assignment, 0, len(displays))
	for _, d := range displays {
		info := d.Info()
		var mon *config.Monitor
		if info.Serial != "" {
			mon = byMatch[strings.ToLower(info.Key())]
		}
		if mon == nil {
			mon = byMatch[strings.ToLower(info.Model)]
		}
		out = append(out, Assignment{Display: d, Monitor: mon})
	}
	return out
}
