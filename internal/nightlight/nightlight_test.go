package nightlight_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"dusk/internal/ddc/ddctest"
	"dusk/internal/nightlight"
)

const schema = "org.gnome.settings-daemon.plugins.color"

func runner(values map[string]string) *ddctest.FakeRunner {
	return &ddctest.FakeRunner{Handler: func(a []string) (string, error) {
		if a[0] == "get" {
			v, ok := values[a[2]]
			if !ok {
				return "", errors.New("unknown key")
			}
			return v + "\n", nil
		}
		return "", nil
	}}
}

var userValues = map[string]string{
	"night-light-enabled":            "false",
	"night-light-schedule-automatic": "true",
	"night-light-schedule-from":      "20.0",
	"night-light-schedule-to":        "6.0",
	"night-light-temperature":        "uint32 2700",
}

func TestSnapshot(t *testing.T) {
	g := nightlight.Gsettings{Runner: runner(userValues)}
	got, err := g.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := nightlight.Original{Enabled: false, Automatic: true, From: 20, To: 6, Temperature: 2700}
	if got != want {
		t.Errorf("got %+v", got)
	}
}

func TestSnapshotErrors(t *testing.T) {
	bad := map[string]string{}
	for k, v := range userValues {
		bad[k] = v
	}
	bad["night-light-temperature"] = "abc"
	if _, err := (nightlight.Gsettings{Runner: runner(bad)}).Snapshot(t.Context()); err == nil {
		t.Error("an invalid temperature should fail")
	}
	if _, err := (nightlight.Gsettings{Runner: runner(map[string]string{})}).Snapshot(t.Context()); err == nil {
		t.Error("gsettings failing should fail")
	}
}

func TestEnableSequenceAndClamp(t *testing.T) {
	fr := runner(nil)
	g := nightlight.Gsettings{Runner: fr}
	if err := g.Enable(t.Context(), 1000); err != nil { // below GNOME's minimum
		t.Fatal(err)
	}
	want := [][]string{
		{"set", schema, "night-light-temperature", "1700"},
		{"set", schema, "night-light-schedule-from", "0.0"},
		{"set", schema, "night-light-schedule-to", "24.0"},
		{"set", schema, "night-light-schedule-automatic", "false"},
		{"set", schema, "night-light-enabled", "true"},
	}
	if !reflect.DeepEqual(fr.Calls(), want) {
		t.Errorf("calls:\n%v", fr.Calls())
	}
}

func TestRestoreWritesOriginalsAndEnabledLast(t *testing.T) {
	fr := runner(nil)
	g := nightlight.Gsettings{Runner: fr}
	orig := nightlight.Original{Enabled: false, Automatic: true, From: 20, To: 6.5, Temperature: 2700}
	if err := g.Restore(t.Context(), orig); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"set", schema, "night-light-temperature", "2700"},
		{"set", schema, "night-light-schedule-from", "20.0"},
		{"set", schema, "night-light-schedule-to", "6.5"},
		{"set", schema, "night-light-schedule-automatic", "true"},
		{"set", schema, "night-light-enabled", "false"},
	}
	if !reflect.DeepEqual(fr.Calls(), want) {
		t.Errorf("calls:\n%v", fr.Calls())
	}
}

func TestSetErrorPropagates(t *testing.T) {
	fr := &ddctest.FakeRunner{Handler: func([]string) (string, error) { return "", errors.New("no dbus") }}
	err := nightlight.Gsettings{Runner: fr}.Enable(t.Context(), 3000)
	if err == nil || !strings.Contains(err.Error(), "no dbus") {
		t.Errorf("err = %v", err)
	}
}

func TestDetect(t *testing.T) {
	b, err := nightlight.Detect(t.Context(), runner(userValues))
	if err != nil || b == nil {
		t.Fatalf("expected the GNOME backend: %v %v", b, err)
	}
	fr := runner(nil) // every get fails: no GNOME night light here
	b, err = nightlight.Detect(t.Context(), fr)
	if b != nil || !errors.Is(err, nightlight.ErrNoBackend) {
		t.Errorf("expected ErrNoBackend, got %v %v", b, err)
	}
	if len(fr.SetCalls()) != 0 {
		t.Error("Detect must never write")
	}
}
