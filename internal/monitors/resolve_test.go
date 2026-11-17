// Filename: internal/monitors/resolve_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package monitors_test

import (
	"testing"

	"dusk/internal/config"
	"dusk/internal/ddc"
	"dusk/internal/ddc/ddctest"
	"dusk/internal/gamma"
	"dusk/internal/monitors"
)

func disp(model, serial, connector string, bus int) ddc.Display {
	return ddctest.NewFakeDisplay(ddc.Info{Model: model, Serial: serial, Connector: connector, Bus: bus})
}

func cfgOf(matches ...string) *config.Config {
	c := &config.Config{}
	for _, m := range matches {
		c.Monitors = append(c.Monitors, config.Monitor{Name: m, Match: m})
	}
	return c
}

func TestNormalizeConnector(t *testing.T) {
	cases := map[string]string{
		"card1-DP-1": "dp-1", "DP-1": "dp-1", "card1-HDMI-A-1": "hdmi-1", "HDMI-1": "hdmi-1", "card0-eDP-1": "edp-1", "": "",
	}
	for in, want := range cases {
		if got := monitors.NormalizeConnector(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestResolveRealSetup(t *testing.T) {
	outs := []gamma.Output{
		{CRTC: 0, Name: "DP-1", Product: "24G4", Serial: "SN-AOC-0001"},
		{CRTC: 1, Name: "HDMI-1", Product: "LG ULTRAWIDE"},
		{CRTC: 2, Name: "DP-3", Product: "LF24T450F", Serial: "SN-SAM-0002"},
	}
	ds := []ddc.Display{
		disp("24G4", "SN-AOC-0001", "card1-DP-1", 5),
		disp("LG ULTRAWIDE", "", "card1-HDMI-A-1", 7),
		disp("LF24T450F", "SN-SAM-0002", "card1-DP-3", 8),
	}
	got := monitors.Resolve(cfgOf("24G4#SN-AOC-0001", "LG ULTRAWIDE"), outs, ds)
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Monitor == nil || got[0].Display.Info().Bus != 5 {
		t.Errorf("AOC = %+v", got[0])
	}
	if got[1].Monitor == nil || got[1].Display.Info().Bus != 7 {
		t.Errorf("LG = %+v", got[1])
	}
	if got[2].Monitor != nil || got[2].Display.Info().Bus != 8 {
		t.Errorf("Samsung should have a display but no config entry: %+v", got[2])
	}
}

func TestResolveSerialBeatsModel(t *testing.T) {
	outs := []gamma.Output{{Name: "DP-1", Product: "X", Serial: "A"}, {Name: "DP-2", Product: "X", Serial: "B"}}
	cfg := cfgOf("X", "x#b")
	got := monitors.Resolve(cfg, outs, nil)
	if got[0].Monitor.Match != "X" || got[1].Monitor.Match != "x#b" {
		t.Fatalf("got %q and %q", got[0].Monitor.Match, got[1].Monitor.Match)
	}
}

func TestResolveSerialInConfigNeedsSerialOnOutput(t *testing.T) {
	got := monitors.Resolve(cfgOf("X#A"), []gamma.Output{{Name: "DP-1", Product: "X"}}, nil)
	if got[0].Monitor != nil {
		t.Fatal("model#serial must not match an output without serial")
	}
}

func TestResolveWithoutDDC(t *testing.T) {
	got := monitors.Resolve(cfgOf("X"), []gamma.Output{{Name: "DP-1", Product: "X"}}, nil)
	if got[0].Display != nil || got[0].Monitor == nil {
		t.Fatalf("%+v", got[0])
	}
}

func TestResolveFallsBackToModelAndSerial(t *testing.T) {
	outs := []gamma.Output{{Name: "weird", Product: "X", Serial: "A"}}
	got := monitors.Resolve(nil, outs, []ddc.Display{disp("x", "a", "card9-other", 3)})
	if got[0].Display == nil || got[0].Display.Info().Bus != 3 {
		t.Fatalf("%+v", got[0])
	}
}

func TestResolveFallbackSkipsAmbiguousModels(t *testing.T) {
	outs := []gamma.Output{{Name: "weird", Product: "X"}}
	ds := []ddc.Display{disp("X", "", "c-a", 1), disp("X", "", "c-b", 2)}
	if got := monitors.Resolve(nil, outs, ds); got[0].Display != nil {
		t.Fatal("two candidates must not be guessed")
	}
}

func TestResolveDisplayUsedOnce(t *testing.T) {
	d := disp("X", "", "card1-DP-1", 5)
	outs := []gamma.Output{{Name: "DP-1", Product: "X"}, {Name: "DP-2", Product: "X"}}
	got := monitors.Resolve(nil, outs, []ddc.Display{d})
	if got[0].Display != d || got[1].Display != nil {
		t.Fatalf("%+v %+v", got[0], got[1])
	}
}
