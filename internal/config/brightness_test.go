// Filename: internal/config/brightness_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package config_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"dusk/internal/config"
)

func monitorToml(extra string) string {
	return "[[monitor]]\nname = \"a\"\nmatch = \"m\"\n" + extra + "\npoints = [{ time = \"08:00\", brightness = 50 }]\n"
}

func TestBrightnessAndFloorDefaults(t *testing.T) {
	cfg, err := config.Parse([]byte(monitorToml("")))
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Monitors[0]
	if m.Brightness != config.BrightnessSoftware || m.Floor != config.DefaultFloor {
		t.Fatalf("defaults = %q/%d", m.Brightness, m.Floor)
	}
}

func TestBrightnessAndFloorValues(t *testing.T) {
	cases := []struct {
		name, extra, wantErr string
		brightness           string
		floor                int
	}{
		{name: "ddc", extra: `brightness = "ddc"`, brightness: "ddc", floor: 10},
		{name: "floor 0", extra: "floor = 0", brightness: "software", floor: 0},
		{name: "floor 50", extra: "floor = 50", brightness: "software", floor: 50},
		{name: "ddc ignores floor", extra: "brightness = \"ddc\"\nfloor = 20", brightness: "ddc", floor: 20},
		{name: "bad backend", extra: `brightness = "gamma"`, wantErr: `monitor "a".brightness: unknown value "gamma"`},
		{name: "floor too high", extra: "floor = 80", wantErr: `monitor "a".floor: 80 is outside 0-50`},
		{name: "floor negative", extra: "floor = -1", wantErr: `monitor "a".floor: -1 is outside 0-50`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := config.Parse([]byte(monitorToml(c.extra)))
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if m := cfg.Monitors[0]; m.Brightness != c.brightness || m.Floor != c.floor {
				t.Fatalf("got %q/%d", m.Brightness, m.Floor)
			}
		})
	}
}

func TestMarshalRoundTripWithNewFields(t *testing.T) {
	src := `
[night]
enabled = true
start = "19:00"
end = "07:00"

[[monitor]]
name = "a"
match = "m1"
brightness = "ddc"
points = [{ time = "08:00", brightness = 50 }]

[[monitor]]
name = "b"
match = "m2"
floor = 25
points = [{ time = "08:00", brightness = 50 }]

[[monitor]]
name = "c"
match = "m3"
points = [{ time = "08:00", brightness = 50 }]
`
	cfg, err := config.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out := config.Marshal(cfg, nil)
	back, err := config.Parse(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !reflect.DeepEqual(cfg, back) {
		t.Fatalf("round trip differs:\n%+v\n%+v\n%s", cfg, back, out)
	}
	if strings.Count(string(out), "floor") != 1 || strings.Count(string(out), "brightness = \"ddc\"") != 1 {
		t.Errorf("defaults must not be written:\n%s", out)
	}
}

func TestLegacyConfigStillLoads(t *testing.T) {
	data, err := os.ReadFile("testdata/legacy-2026-11-13.toml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Monitors) != 2 || cfg.Night.BlueGain != 30 || len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "night.fallback is ignored") {
		t.Fatalf("cfg = %+v", cfg)
	}
	for _, m := range cfg.Monitors {
		if m.Brightness != config.BrightnessSoftware || m.Floor != config.DefaultFloor {
			t.Errorf("%s: %q/%d", m.Name, m.Brightness, m.Floor)
		}
	}
}
