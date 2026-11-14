package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validTOML = `
poll_interval = "10s"

[night]
start = "21:00"
end = "07:00"
ramp = "45m"
blue_gain = 50
fallback = "off"

[[monitor]]
name = "desk"
match = "MODEL-A#SERIAL-1"
points = [
  { time = "22:00", brightness = 20 },
  { time = "08:00", brightness = 70 },
  { time = "18:00", brightness = 40 },
]

[[monitor]]
name = "side"
match = "MODEL B"
points = [{ time = "00:00", brightness = 50 }]
`

func TestParseValid(t *testing.T) {
	cfg, err := Parse([]byte(validTOML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PollInterval != 10*time.Second || cfg.RescanInterval != DefaultRescanInterval {
		t.Errorf("intervals: %v %v", cfg.PollInterval, cfg.RescanInterval)
	}
	n := cfg.Night
	if !n.Enabled || n.Start != 21*60 || n.End != 7*60 || n.Ramp != 45*time.Minute ||
		n.BlueGain != 50 || n.RedGain != 100 || n.GreenGain != 100 || n.Fallback != FallbackOff {
		t.Errorf("unexpected night: %+v", n)
	}
	if len(cfg.Monitors) != 2 {
		t.Fatalf("monitors: %d", len(cfg.Monitors))
	}
	got := cfg.Monitors[0].Points
	want := []Point{{8 * 60, 70}, {18 * 60, 40}, {22 * 60, 20}}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("points not sorted: %v", got)
			break
		}
	}
}

func TestParseDefaultsWithoutNight(t *testing.T) {
	cfg, err := Parse([]byte(`
[[monitor]]
name = "a"
match = "A"
points = [{ time = "08:00", brightness = 10 }]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Night.Enabled {
		t.Error("without [night], night mode must be off")
	}
	if cfg.Night.Fallback != FallbackAuto {
		t.Errorf("default fallback = %q", cfg.Night.Fallback)
	}
	if cfg.VerifyInterval != DefaultVerifyInterval || cfg.RescanInterval != DefaultRescanInterval {
		t.Errorf("defaults: verify %v rescan %v", cfg.VerifyInterval, cfg.RescanInterval)
	}
	if cfg.PollInterval != DefaultPollInterval {
		t.Errorf("default poll interval: %v", cfg.PollInterval)
	}
}

func TestParseInvalid(t *testing.T) {
	mon := func(extra string) string {
		return "[[monitor]]\nname = \"a\"\nmatch = \"A\"\n" + extra + "\n"
	}
	ok := `points = [{ time = "08:00", brightness = 10 }]`
	tests := []struct {
		name, toml, want string
	}{
		{"bad time", mon(`points = [{ time = "8:00", brightness = 10 }]`), "not in HH:MM format"},
		{"hour 25", mon(`points = [{ time = "25:00", brightness = 10 }]`), "outside 00:00-23:59"},
		{"brightness 101", mon(`points = [{ time = "08:00", brightness = 101 }]`), "outside 0-100"},
		{"missing brightness", mon(`points = [{ time = "08:00" }]`), "brightness is required"},
		{"no points", mon(``), "at least one entry"},
		{"repeated point", mon(`points = [{ time = "08:00", brightness = 1 }, { time = "08:00", brightness = 2 }]`), "repeated"},
		{"duplicate name", mon(ok) + mon(ok), "duplicate name"},
		{"duplicate match", mon(ok) + "[[monitor]]\nname = \"b\"\nmatch = \"A\"\n" + ok + "\n", "duplicate match"},
		{"unknown key", "pol_interval = \"1s\"\n" + mon(ok), "unknown key: pol_interval"},
		{"bad duration", "poll_interval = \"abc\"\n" + mon(ok), "invalid duration"},
		{"zero duration", "poll_interval = \"0s\"\n" + mon(ok), "must be positive"},
		{"gain 120", "[night]\nstart = \"21:00\"\nend = \"07:00\"\nblue_gain = 120\n" + mon(ok), "blue_gain"},
		{"bad fallback", "[night]\nstart = \"21:00\"\nend = \"07:00\"\nfallback = \"x\"\n" + mon(ok), "fallback"},
		{"old fallback value", "[night]\nstart = \"21:00\"\nend = \"07:00\"\nfallback = \"gnome\"\n" + mon(ok), "fallback"},
		{"start equals end", "[night]\nstart = \"21:00\"\nend = \"21:00\"\n" + mon(ok), "must differ"},
		{"night without times", "[night]\n" + mon(ok), "night.start"},
		{"ramp longer than window", "[night]\nstart = \"21:00\"\nend = \"21:10\"\nramp = \"30m\"\n" + mon(ok), "longer than the night window"},
		{"ramp overlaps next ramp-up", "[night]\nstart = \"07:10\"\nend = \"07:00\"\nramp = \"30m\"\n" + mon(ok), "runs into the next ramp-up"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.toml))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !IsValidation(err) {
				t.Fatalf("expected *ValidationError, got %T: %v", err, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestParseAggregatesErrors(t *testing.T) {
	_, err := Parse([]byte(`
poll_interval = "abc"
[[monitor]]
name = "a"
match = "A"
points = [{ time = "99:00", brightness = 500 }]
`))
	ve, ok := err.(*ValidationError)
	if !ok || len(ve.Problems) < 2 {
		t.Fatalf("expected several aggregated problems: %v", err)
	}
}

func TestParseSyntaxError(t *testing.T) {
	_, err := Parse([]byte("this is not toml ["))
	if err == nil || IsValidation(err) {
		t.Fatalf("expected a syntax error, got %v", err)
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte(validTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); !os.IsNotExist(err) {
		t.Errorf("expected a missing-file error, got %v", err)
	}
}

func TestPerMonitorNightOverrides(t *testing.T) {
	cfg, err := Parse([]byte(`
[night]
start = "21:00"
end = "07:00"
blue_gain = 60
green_gain = 100

[[monitor]]
name = "warm"
match = "A"
night = { blue_gain = 40, green_gain = 90 }
points = [{ time = "08:00", brightness = 10 }]

[[monitor]]
name = "plain"
match = "B"
points = [{ time = "08:00", brightness = 10 }]
`))
	if err != nil {
		t.Fatal(err)
	}
	warm := cfg.Monitors[0].NightFor(cfg.Night)
	if warm.BlueGain != 40 || warm.GreenGain != 90 || warm.RedGain != 100 {
		t.Errorf("overridden gains = %+v", warm)
	}
	if warm.Start != cfg.Night.Start || warm.End != cfg.Night.End || warm.Ramp != cfg.Night.Ramp || warm.Fallback != cfg.Night.Fallback {
		t.Errorf("the window, ramp and fallback must stay global: %+v", warm)
	}
	if got := cfg.Monitors[1].NightFor(cfg.Night); got != cfg.Night {
		t.Errorf("a monitor without overrides inherits everything: %+v", got)
	}
	if cfg.Night.BlueGain != 60 {
		t.Error("NightFor must not modify the global config")
	}
}

func TestPerMonitorNightInvalid(t *testing.T) {
	base := "[[monitor]]\nname = \"a\"\nmatch = \"A\"\npoints = [{ time = \"08:00\", brightness = 10 }]\n"
	for name, night := range map[string]string{
		"out of range": "night = { blue_gain = 120 }\n",
		"negative":     "night = { red_gain = -1 }\n",
		"unknown key":  "night = { blu_gain = 40 }\n",
	} {
		toml := strings.Replace(base, "points", night+"points", 1)
		if _, err := Parse([]byte(toml)); err == nil || !IsValidation(err) {
			t.Errorf("%s: expected a validation error, got %v", name, err)
		}
	}
}

func TestMarshalRoundTripWithMonitorNight(t *testing.T) {
	blue, red := 45, 90
	cfg := &Config{
		PollInterval: DefaultPollInterval, RescanInterval: DefaultRescanInterval, VerifyInterval: DefaultVerifyInterval,
		Night: Night{Enabled: true, Start: 21 * 60, End: 7 * 60, Ramp: 30 * time.Minute, BlueGain: 60, RedGain: 100, GreenGain: 100, Fallback: FallbackAuto},
		Monitors: []Monitor{
			{Name: "a", Match: "A", Points: []Point{{0, 50}}, Night: MonitorNight{BlueGain: &blue, RedGain: &red}},
			{Name: "b", Match: "B", Points: []Point{{0, 50}}},
		},
	}
	out := roundTrip(t, cfg, nil)
	if !strings.Contains(out, "night = { blue_gain = 45, red_gain = 90 }") {
		t.Errorf("inline table missing:\n%s", out)
	}
	if strings.Count(out, "night = {") != 1 {
		t.Errorf("a monitor without overrides must not get an inline table:\n%s", out)
	}
}

func TestVerifyIntervalIsParsedAndValidated(t *testing.T) {
	mon := "[[monitor]]\nname = \"a\"\nmatch = \"A\"\npoints = [{ time = \"08:00\", brightness = 1 }]\n"
	cfg, err := Parse([]byte("verify_interval = \"15m\"\n" + mon))
	if err != nil || cfg.VerifyInterval != 15*time.Minute {
		t.Fatalf("%v %v", cfg, err)
	}
	for _, bad := range []string{`verify_interval = "0s"`, `verify_interval = "soon"`} {
		if _, err := Parse([]byte(bad + "\n" + mon)); err == nil || !IsValidation(err) {
			t.Errorf("%s should be rejected: %v", bad, err)
		}
	}
	if DefaultRescanInterval < 30*time.Minute {
		t.Errorf("a full rescan runs `ddcutil detect`, the heaviest bus operation, so it must stay rare: %v", DefaultRescanInterval)
	}
}
