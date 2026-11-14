package monitors_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"dusk/internal/config"
	"dusk/internal/ddc"
	"dusk/internal/ddc/ddctest"
	"dusk/internal/monitors"
)

func runner(t *testing.T, failBus string) *ddctest.FakeRunner {
	t.Helper()
	detect, err := os.ReadFile("../ddc/testdata/detect_brief.txt")
	if err != nil {
		t.Fatal(err)
	}
	return &ddctest.FakeRunner{Handler: func(a []string) (string, error) {
		switch a[0] {
		case "detect":
			return string(detect), nil
		case "getvcp":
			if a[3] == failBus {
				return "", errors.New("monitor is asleep")
			}
			return "VCP " + a[1] + " C 30 100\n", nil
		}
		return "", errors.New("unexpected call: " + a[0])
	}}
}

func TestDiscover(t *testing.T) {
	fr := runner(t, "")
	ds, err := monitors.Discover(context.Background(), fr)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 3 || ds[0].Info().Bus != 5 || ds[1].Info().Bus != 7 || ds[2].Info().Bus != 8 {
		t.Fatalf("displays: %+v", ds)
	}
	for _, d := range ds {
		if c := d.Caps(); !c.Brightness || !c.RGB {
			t.Errorf("%s: caps %+v", d.Info().Model, c)
		}
	}
	if n := len(fr.Calls()); n != 1+4*3 {
		t.Errorf("calls = %d; want 13", n)
	}
	if len(fr.SetCalls()) != 0 {
		t.Error("Discover must not write")
	}
}

func TestDiscoverOneMonitorFailing(t *testing.T) {
	ds, err := monitors.Discover(context.Background(), runner(t, "7"))
	if err != nil || len(ds) != 3 {
		t.Fatalf("%v %d", err, len(ds))
	}
	if c := ds[1].Caps(); c.Brightness || c.RGB {
		t.Errorf("bus 7 should have no capabilities: %+v", c)
	}
	if !ds[0].Caps().Brightness || !ds[2].Caps().Brightness {
		t.Error("the other monitors must keep working")
	}
}

func TestDiscoverDetectFails(t *testing.T) {
	fr := &ddctest.FakeRunner{Handler: func([]string) (string, error) { return "", errors.New("no ddcutil") }}
	if _, err := monitors.Discover(context.Background(), fr); err == nil {
		t.Error("expected an error")
	}
}

func fake(model, serial string, bus int) ddc.Display {
	return ddctest.NewFakeDisplay(ddc.Info{Model: model, Serial: serial, Bus: bus})
}

func TestMatch(t *testing.T) {
	cfg := &config.Config{Monitors: []config.Monitor{
		{Name: "aoc", Match: "24g4#sn-aoc-0001"},
		{Name: "any-24g4", Match: "24G4"},
		{Name: "lg", Match: "LG UltraWide"},
	}}
	displays := []ddc.Display{
		fake("24G4", "SN-AOC-0001", 5),
		fake("24G4", "OTHER", 9),
		fake("LG ULTRAWIDE", "", 7),
		fake("LF24T450F", "SN-SAM-0003", 8),
	}
	got := monitors.Match(cfg, displays)
	names := make([]string, len(got))
	for i, a := range got {
		if a.Monitor != nil {
			names[i] = a.Monitor.Name
		}
	}
	want := []string{"aoc", "any-24g4", "lg", ""}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("display %d: %q; want %q", i, names[i], want[i])
		}
	}
}

func TestMatchSurvivesPortChange(t *testing.T) {
	cfg := &config.Config{Monitors: []config.Monitor{{Name: "lg", Match: "LG ULTRAWIDE"}}}
	for _, bus := range []int{7, 3, 11} {
		got := monitors.Match(cfg, []ddc.Display{fake("LG ULTRAWIDE", "", bus)})
		if got[0].Monitor == nil || got[0].Monitor.Name != "lg" {
			t.Errorf("bus %d lost the match", bus)
		}
	}
}

func TestMatchNilConfig(t *testing.T) {
	got := monitors.Match(nil, []ddc.Display{fake("X", "", 1)})
	if len(got) != 1 || got[0].Monitor != nil {
		t.Errorf("%+v", got)
	}
}
