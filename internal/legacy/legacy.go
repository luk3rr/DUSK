// Filename: internal/legacy/legacy.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

// Package legacy undoes what older dusk versions did to the monitors' own
// memory. Older versions wrote RGB gains over DDC/CI for night mode, and those
// values stay in the monitor after the computer is turned off. ResetDDC is the
// only code in dusk that writes the RGB gain VCPs, and only after the user
// confirms (ADR-15).
package legacy

import (
	"context"
	"errors"
	"fmt"

	"dusk/internal/ddc"
	"dusk/internal/state"
)

var rgbVCPs = [3]ddc.VCP{ddc.VCPRedGain, ddc.VCPGreenGain, ddc.VCPBlueGain}

// Item is what resetting one monitor would do.
type Item struct {
	Display   ddc.Display
	Key       string
	Current   [3]int
	Target    [3]int
	FromState bool // Target is the original saved by an older dusk (otherwise the monitor's maximum)
	// NoOriginal is true when nothing was saved and the maximum was not requested:
	// the item is informational and must not be applied.
	NoOriginal bool
}

// Normal reports whether the monitor already holds its target gains.
func (i Item) Normal() bool { return !i.NoOriginal && i.Current == i.Target }

// Plan reads (never writes) the RGB gains of every display that has color
// control and works out where they should go: the original saved in st. With
// nothing saved the target is each channel's maximum only when toMax is true;
// otherwise the monitor is left out, because without a saved original there is
// no way to know what "normal" is for that monitor (many are calibrated below
// 100). A display that cannot be read is reported in the joined error.
func Plan(ctx context.Context, displays []ddc.Display, st state.State, toMax bool) ([]Item, error) {
	var items []Item
	var errs []error
	for _, d := range displays {
		caps := d.Caps()
		if !caps.RGB {
			continue
		}
		key := d.Info().Key()
		it := Item{Display: d, Key: key, Target: caps.RGBMax}
		if o, ok := st.Originals[key]; ok {
			it.Target, it.FromState = [3]int{o.R, o.G, o.B}, true
		} else if !toMax {
			it.NoOriginal = true
		}
		failed := false
		for ch, vcp := range rgbVCPs {
			v, _, err := d.Get(ctx, vcp)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", key, err))
				failed = true
				break
			}
			it.Current[ch] = v
		}
		if !failed {
			items = append(items, it)
		}
	}
	return items, errors.Join(errs...)
}

// Apply writes the target gains of one item (clamped by the display to its range).
func Apply(ctx context.Context, it Item) error {
	if it.NoOriginal {
		return fmt.Errorf("%s: no saved original and --to-max not requested", it.Key)
	}
	for ch, vcp := range rgbVCPs {
		if err := it.Display.Set(ctx, vcp, it.Target[ch]); err != nil {
			return fmt.Errorf("%s: %w", it.Key, err)
		}
	}
	return nil
}

// Forget removes the saved original of a monitor once it has been restored.
func Forget(store state.Store, key string) error {
	st, err := store.Load()
	if err != nil {
		return err
	}
	if _, ok := st.Originals[key]; !ok {
		return nil
	}
	delete(st.Originals, key)
	return store.Save(st)
}
