// Filename: internal/config/temperature_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package config_test

import (
	"reflect"
	"strings"
	"testing"

	"dusk/internal/config"
)

const tempToml = `
[night]
enabled = true
start = "21:00"
end = "07:00"
temperature = 3400
blue_gain = 90 # ignored: the temperature wins

[[monitor]]
name = "a"
match = "A"
night = { temperature = 2600 }
points = [{ time = "08:00", brightness = 50 }]

[[monitor]]
name = "b"
match = "B"
night = { temperature = 3000, green_gain = 50 }
points = [{ time = "08:00", brightness = 50 }]

[[monitor]]
name = "c"
match = "C"
points = [{ time = "08:00", brightness = 50 }]
`

func TestTemperatureDerivesTheGainsAndWinsOverThem(t *testing.T) {
	cfg, err := config.Parse([]byte(tempToml))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b := config.GainsForKelvin(3400)
	n := cfg.Night
	if n.Temperature != 3400 || n.RedGain != r || n.GreenGain != g || n.BlueGain != b {
		t.Fatalf("night = %+v, want gains %d %d %d", n, r, g, b)
	}
	a := cfg.Monitors[0].NightFor(cfg.Night)
	r2, g2, b2 := config.GainsForKelvin(2600)
	if a.Temperature != 2600 || a.RedGain != r2 || a.GreenGain != g2 || a.BlueGain != b2 {
		t.Errorf("monitor a = %+v", a)
	}
	bb := cfg.Monitors[1].NightFor(cfg.Night)
	_, g3, b3 := config.GainsForKelvin(3000)
	if bb.GreenGain != 50 || bb.BlueGain != b3 || g3 == 50 {
		t.Errorf("explicit gains apply on top of the monitor temperature: %+v", bb)
	}
	if c := cfg.Monitors[2].NightFor(cfg.Night); c != cfg.Night {
		t.Errorf("a monitor without overrides inherits the global night: %+v", c)
	}
}

func TestTemperatureOutOfRangeIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, night, want string }{
		{"global too warm", "temperature = 1000", "night.temperature: 1000 is outside 1700-6500"},
		{"global too cold", "temperature = 9000", "night.temperature: 9000 is outside"},
	} {
		src := "[night]\nstart = \"21:00\"\nend = \"07:00\"\n" + tc.night + "\n" + monitorToml("")
		if _, err := config.Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
	src := monitorToml("night = { temperature = 500 }")
	if _, err := config.Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), `monitor "a".night.temperature: 500 is outside`) {
		t.Errorf("monitor: err = %v", err)
	}
}

func TestTemperatureRoundTripsThroughMarshal(t *testing.T) {
	cfg, err := config.Parse([]byte(tempToml))
	if err != nil {
		t.Fatal(err)
	}
	out := config.Marshal(cfg, nil)
	if !strings.Contains(string(out), "temperature = 3400") || strings.Contains(string(out), "blue_gain  =") {
		t.Errorf("the global gains must be replaced by the temperature:\n%s", out)
	}
	back, err := config.Parse(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !reflect.DeepEqual(cfg, back) {
		t.Fatalf("round trip differs:\n%+v\n%+v\n%s", cfg, back, out)
	}
}

func TestExistingGainsStillWorkWithoutATemperature(t *testing.T) {
	cfg, err := config.Parse([]byte("[night]\nstart = \"21:00\"\nend = \"07:00\"\nblue_gain = 40\n" + monitorToml("")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Night.Temperature != 0 || cfg.Night.BlueGain != 40 {
		t.Fatalf("night = %+v", cfg.Night)
	}
}
