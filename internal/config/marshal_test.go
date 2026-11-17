package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func roundTrip(t *testing.T, cfg *Config, notes map[string]string) string {
	t.Helper()
	data := Marshal(cfg, notes)
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Marshal produced an invalid file: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Fatalf("round trip differs\n got: %+v\nwant: %+v\n%s", got, cfg, data)
	}
	return string(data)
}

func TestMarshalRoundTrip(t *testing.T) {
	cfg := &Config{
		PollInterval:   45 * time.Second,
		RescanInterval: 10 * time.Minute,
		VerifyInterval: 7 * time.Minute,
		Night: Night{Enabled: true, Start: 21*60 + 30, End: 6*60 + 15, Ramp: time.Hour,
			BlueGain: 55, RedGain: 95, GreenGain: 100},
		Monitors: []Monitor{
			{Name: "desk", Match: "MODEL-A#SERIAL-1", Brightness: BrightnessSoftware, Floor: DefaultFloor, Points: []Point{{480, 70}, {1080, 40}, {1320, 20}}},
			{Name: "side", Match: "MODEL B", Brightness: BrightnessSoftware, Floor: DefaultFloor, Points: []Point{{0, 50}}},
		},
	}
	out := roundTrip(t, cfg, map[string]string{"desk": "Vendor Model A (serial SERIAL-1)\nDP-1, i2c bus 5"})
	for _, want := range []string{"# Vendor Model A (serial SERIAL-1)", "# DP-1, i2c bus 5", `ramp       = "1h"`, `poll_interval   = "45s"`, `rescan_interval = "10m"`, `verify_interval = "7m"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestMarshalNightDisabledAndNoMonitors(t *testing.T) {
	cfg := &Config{
		PollInterval: DefaultPollInterval, RescanInterval: DefaultRescanInterval, VerifyInterval: DefaultVerifyInterval,
		Night: Night{Enabled: false, Ramp: DefaultRamp, BlueGain: 60, RedGain: 100, GreenGain: 100},
	}
	out := roundTrip(t, cfg, nil)
	if !strings.Contains(out, "enabled    = false") || strings.Contains(out, "start") {
		t.Errorf("a disabled night mode should not write start/end:\n%s", out)
	}
}

func TestMarshalQuotesSpecialCharacters(t *testing.T) {
	cfg := &Config{
		PollInterval: DefaultPollInterval, RescanInterval: DefaultRescanInterval, VerifyInterval: DefaultVerifyInterval,
		Night: Night{Ramp: DefaultRamp, BlueGain: 60, RedGain: 100, GreenGain: 100},
		Monitors: []Monitor{
			{Name: `we"ird\name`, Match: "Model \"X\" \\ 27\"\t#tab", Brightness: BrightnessSoftware, Floor: DefaultFloor, Points: []Point{{0, 10}}},
		},
	}
	roundTrip(t, cfg, nil)
}

func TestMarshalDefaultsForUnsetRampAndFallback(t *testing.T) {
	cfg := &Config{PollInterval: DefaultPollInterval, RescanInterval: DefaultRescanInterval, VerifyInterval: DefaultVerifyInterval,
		Night: Night{BlueGain: 60, RedGain: 100, GreenGain: 100}}
	got, err := Parse(Marshal(cfg, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Night.Ramp != DefaultRamp {
		t.Errorf("defaults not applied: %+v", got.Night)
	}
}

func TestFormatDuration(t *testing.T) {
	for in, want := range map[time.Duration]string{
		30 * time.Second: "30s", 5 * time.Minute: "5m", time.Hour: "1h", 90 * time.Minute: "1h30m", 2*time.Hour + 5*time.Second: "2h0m5s",
	} {
		if got := FormatDuration(in); got != want {
			t.Errorf("FormatDuration(%v) = %q; want %q", in, got, want)
		}
		if d, err := time.ParseDuration(FormatDuration(in)); err != nil || d != in {
			t.Errorf("%q does not parse back to %v", FormatDuration(in), in)
		}
	}
}

func TestParseAndFormatClock(t *testing.T) {
	if m, err := ParseClock("07:05"); err != nil || m != 425 || FormatClock(m) != "07:05" {
		t.Errorf("%d %v", m, err)
	}
	for _, bad := range []string{"", "7:05", "24:00", "12:60", "ab:cd", "12-30"} {
		if _, err := ParseClock(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
