// Filename: internal/legacy/legacy_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package legacy_test

import (
	"context"
	"path/filepath"
	"testing"

	"dusk/internal/ddc"
	"dusk/internal/ddc/ddctest"
	"dusk/internal/legacy"
	"dusk/internal/state"
)

func display(model, serial string, rgb [3]int) *ddctest.FakeDisplay {
	d := ddctest.NewFakeDisplay(ddc.Info{Model: model, Serial: serial, Bus: 5})
	d.SetValue(ddc.VCPRedGain, rgb[0])
	d.SetValue(ddc.VCPGreenGain, rgb[1])
	d.SetValue(ddc.VCPBlueGain, rgb[2])
	return d
}

func TestPlanReadsAndNeverWrites(t *testing.T) {
	a := display("24G4", "S1", [3]int{50, 50, 50})
	b := display("LG", "", [3]int{100, 100, 100})
	noRGB := ddctest.NewFakeDisplay(ddc.Info{Model: "Plain"})
	noRGB.DisplayCaps.RGB = false
	st := state.New()
	st.Originals["24G4#S1"] = state.RGB{R: 90, G: 95, B: 100}
	items, err := legacy.Plan(context.Background(), []ddc.Display{a, b, noRGB}, st, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Current != [3]int{50, 50, 50} || items[0].Target != [3]int{90, 95, 100} || !items[0].FromState || items[0].Normal() {
		t.Errorf("a = %+v", items[0])
	}
	if items[1].Target != [3]int{100, 100, 100} || items[1].FromState || !items[1].Normal() {
		t.Errorf("b = %+v", items[1])
	}
	if len(a.Writes()) != 0 || len(b.Writes()) != 0 {
		t.Fatal("planning must not write")
	}
}

// unreadable is a display whose reads time out.
type unreadable struct{ *ddctest.FakeDisplay }

func (unreadable) Get(context.Context, ddc.VCP) (int, int, error) {
	return 0, 0, context.DeadlineExceeded
}

func TestPlanReportsAnUnreadableDisplayAndKeepsTheOthers(t *testing.T) {
	bad := unreadable{display("Bad", "", [3]int{1, 1, 1})}
	good := display("Good", "", [3]int{50, 50, 50})
	items, err := legacy.Plan(context.Background(), []ddc.Display{bad, good}, state.New(), true)
	if err == nil || len(items) != 1 || items[0].Key != "Good" {
		t.Fatalf("items = %+v err = %v", items, err)
	}
}

func TestApplyWritesOnlyTheThreeGains(t *testing.T) {
	d := display("24G4", "S1", [3]int{50, 50, 50})
	items, _ := legacy.Plan(context.Background(), []ddc.Display{d}, state.New(), true)
	if err := legacy.Apply(context.Background(), items[0]); err != nil {
		t.Fatal(err)
	}
	if got := d.Writes(); len(got) != 3 {
		t.Fatalf("writes = %+v", got)
	}
	for _, w := range d.Writes() {
		if w.VCP != ddc.VCPRedGain && w.VCP != ddc.VCPGreenGain && w.VCP != ddc.VCPBlueGain {
			t.Fatalf("unexpected VCP %s", w.VCP)
		}
		if w.Value != 100 {
			t.Fatalf("write = %+v", w)
		}
	}
}

func TestForgetRemovesOnlyThatMonitor(t *testing.T) {
	store := state.Store{Path: filepath.Join(t.TempDir(), "state.json")}
	st := state.New()
	st.Originals["A"] = state.RGB{R: 1, G: 2, B: 3}
	st.Originals["B"] = state.RGB{R: 4, G: 5, B: 6}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Forget(store, "A"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Load()
	if _, ok := got.Originals["A"]; ok || len(got.Originals) != 1 {
		t.Fatalf("originals = %+v", got.Originals)
	}
	if err := legacy.Forget(store, "missing"); err != nil {
		t.Fatal(err)
	}
}

func TestWithoutASavedOriginalNothingIsAssumed(t *testing.T) {
	d := display("24G4", "S1", [3]int{50, 50, 50})
	items, err := legacy.Plan(context.Background(), []ddc.Display{d}, state.New(), false)
	if err != nil || len(items) != 1 || !items[0].NoOriginal || items[0].Normal() {
		t.Fatalf("items = %+v err = %v", items, err)
	}
	if err := legacy.Apply(context.Background(), items[0]); err == nil {
		t.Fatal("Apply must refuse an item without an original")
	}
	if len(d.Writes()) != 0 {
		t.Fatal("nothing may be written")
	}
}
