// Filename: internal/config/kelvin_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package config_test

import (
	"testing"

	"dusk/internal/config"
)

func TestGainsForKelvin(t *testing.T) {
	r, g, b := config.GainsForKelvin(config.NeutralKelvin)
	if r != 100 || g != 100 || b != 100 {
		t.Fatalf("neutral = %d %d %d", r, g, b)
	}
	// Warmer means less blue first, then less green; red stays at 100 below neutral.
	prevG, prevB := 100, 100
	for k := config.NeutralKelvin - 100; k >= config.MinKelvin; k -= 100 {
		r, g, b := config.GainsForKelvin(k)
		if r < 99 || g > prevG || b > prevB || b > g {
			t.Fatalf("%d K = %d %d %d (previous g %d b %d)", k, r, g, b, prevG, prevB)
		}
		prevG, prevB = g, b
	}
	_, g, b = config.GainsForKelvin(3400)
	if g < 60 || g > 85 || b < 35 || b > 65 {
		t.Errorf("3400 K should be a clear but moderate warm tone, got g=%d b=%d", g, b)
	}
	_, _, bMin := config.GainsForKelvin(config.MinKelvin)
	if bMin > 20 {
		t.Errorf("the warmest setting should remove most blue, got %d", bMin)
	}
	// Out of range values are clamped.
	if lo, hi := [3]int{}, [3]int{}; true {
		lo[0], lo[1], lo[2] = config.GainsForKelvin(100)
		hi[0], hi[1], hi[2] = config.GainsForKelvin(config.MinKelvin)
		if lo != hi {
			t.Errorf("below the minimum must clamp: %v vs %v", lo, hi)
		}
	}
	if r1, g1, b1 := config.GainsForKelvin(20000); r1 != 100 || g1 != 100 || b1 != 100 {
		t.Errorf("clamp high = %d %d %d", r1, g1, b1)
	}
}
