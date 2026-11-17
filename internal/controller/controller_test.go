// Filename: internal/controller/controller_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"dusk/internal/config"
	"dusk/internal/ddc"
	"dusk/internal/ddc/ddctest"
	"dusk/internal/gamma"
	"dusk/internal/gamma/gammatest"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }
func (c *clock) Add(d time.Duration) {
	c.t = c.t.Add(d)
}

func at(h, m int) time.Time { return time.Date(2026, 11, 17, h, m, 0, 0, time.Local) }

const baseToml = `
poll_interval = "30s"

[night]
enabled = true
start = "19:00"
end = "07:00"
ramp = "30m"
blue_gain = 30
red_gain = 100
green_gain = 100

[[monitor]]
name = "desk"
match = "24G4#SN1"
%s
points = [{ time = "00:00", brightness = %d }]
`

func parse(t *testing.T, extra string, brightness int) *config.Config {
	t.Helper()
	body := strings.Replace(baseToml, "%s", extra, 1)
	body = strings.Replace(body, "%d", itoa(brightness), 1)
	cfg, err := config.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

var aoc = gamma.Output{CRTC: 0, Name: "DP-1", Vendor: "AOC", Product: "24G4", Serial: "SN1"}

type rig struct {
	t      *testing.T
	c      *Controller
	g      *gammatest.Fake
	clk    *clock
	finds  int // Discover calls
	displs []ddc.Display
}

func newRig(t *testing.T, cfg *config.Config, now time.Time, outs ...gamma.Output) *rig {
	t.Helper()
	if len(outs) == 0 {
		outs = []gamma.Output{aoc}
	}
	r := &rig{t: t, g: gammatest.New(outs...), clk: &clock{t: now}}
	r.c = New(Options{
		Config: cfg,
		Gamma:  r.g,
		Discover: func(context.Context) ([]ddc.Display, error) {
			r.finds++
			return r.displs, nil
		},
		Now:   r.clk.Now,
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return r
}

func (r *rig) tick() { r.c.Tick(context.Background()) }

func (r *rig) ramp() gamma.Ramp {
	r.t.Helper()
	g, err := r.g.Get(context.Background(), aoc)
	if err != nil {
		r.t.Fatal(err)
	}
	return g
}

// end returns the last (brightest) entry of each channel.
func end(r gamma.Ramp) (uint16, uint16, uint16) {
	n := r.Len() - 1
	return r.R[n], r.G[n], r.B[n]
}

func near(got uint16, want float64) bool {
	d := float64(got) - want
	return d > -2 && d < 2
}

func TestNightColorIsAppliedBySoftwareOnly(t *testing.T) {
	r := newRig(t, parse(t, "", 100), at(2, 0))
	r.tick()
	red, green, blue := end(r.ramp())
	if red != 65535 || green != 65535 || !near(blue, 65535*0.30) {
		t.Fatalf("ramp end = %d/%d/%d", red, green, blue)
	}
	if r.finds != 0 {
		t.Fatalf("Discover was called %d times; software monitors never need ddcutil", r.finds)
	}
	st := r.c.Status()
	if len(st.Monitors) != 1 || st.Monitors[0].Gamma != (RGB{R: 100, G: 100, B: 30}) || st.Monitors[0].Output != "DP-1" || st.Monitors[0].DDC {
		t.Fatalf("status = %+v", st.Monitors)
	}
}

func TestDaytimeKeepsTheIdentityRamp(t *testing.T) {
	r := newRig(t, parse(t, "", 100), at(12, 0))
	r.tick()
	if !r.ramp().Equal(gamma.Identity(gammatest.Size)) {
		t.Fatal("ramp should be the identity at noon with 100% brightness")
	}
}

func TestSoftwareBrightnessUsesTheFloor(t *testing.T) {
	r := newRig(t, parse(t, "floor = 20", 0), at(12, 0))
	r.tick()
	red, _, _ := end(r.ramp())
	if !near(red, 65535*0.20) {
		t.Fatalf("brightness 0%% with floor 20 should scale to 20%%, got %d", red)
	}
	if st := r.c.Status().Monitors[0]; st.Scale != 20 || st.Current != 0 || st.Floor != 20 {
		t.Fatalf("status = %+v", st)
	}
}

func TestChangesAreOnePointAtATime(t *testing.T) {
	r := newRig(t, parse(t, "floor = 0", 50), at(2, 0))
	r.tick()
	hist := r.g.History(0)
	if len(hist) < 20 {
		t.Fatalf("expected a gradual transition, got %d writes", len(hist))
	}
	prev := gamma.Identity(gammatest.Size)
	for i, h := range hist {
		for ch, pair := range [][2][]uint16{{prev.R, h.R}, {prev.G, h.G}, {prev.B, h.B}} {
			last := len(pair[0]) - 1
			d := int(pair[0][last]) - int(pair[1][last])
			if d < 0 {
				d = -d
			}
			// One point of gain is 1% of full scale; one point of brightness is up to 1%.
			if d > 656*2 {
				t.Fatalf("step %d channel %d jumped by %d", i, ch, d)
			}
		}
		prev = h
	}
}

func TestSteadyStateDoesNotWriteAgain(t *testing.T) {
	r := newRig(t, parse(t, "", 100), at(2, 0))
	r.tick()
	n := r.g.Sets()
	r.clk.Add(30 * time.Second)
	r.tick()
	if got := r.g.Sets(); got != n {
		t.Fatalf("a tick with nothing to change wrote %d more ramps", got-n)
	}
}

func TestIgnoresScreensWithoutAConfigEntry(t *testing.T) {
	other := gamma.Output{CRTC: 1, Name: "DP-2", Product: "Other", Serial: "X"}
	r := newRig(t, parse(t, "", 100), at(2, 0), aoc, other)
	r.tick()
	if n := len(r.g.History(1)); n != 0 {
		t.Fatalf("a screen without a monitor entry was written %d times", n)
	}
}

func TestPauseRestoresTheNormalColorsAndResumeReappliesThem(t *testing.T) {
	r := newRig(t, parse(t, "", 100), at(2, 0))
	r.tick()
	r.c.Pause()
	r.tick()
	if !r.ramp().Equal(gamma.Identity(gammatest.Size)) {
		t.Fatal("pause must put the colors back to normal")
	}
	n := r.g.Sets()
	r.tick()
	if r.g.Sets() != n {
		t.Fatal("a paused controller must not write")
	}
	r.c.Resume()
	r.tick()
	if _, _, blue := end(r.ramp()); !near(blue, 65535*0.30) {
		t.Fatalf("resume must reapply the night colors, blue = %d", blue)
	}
}

func TestShutdownRestoresTheIdentityRamp(t *testing.T) {
	r := newRig(t, parse(t, "", 100), at(2, 0))
	r.tick()
	r.c.Shutdown()
	if !r.ramp().Equal(gamma.Identity(gammatest.Size)) {
		t.Fatal("shutdown must restore the normal ramp")
	}
}

func TestReappliesWhenAnotherProgramChangesTheRamp(t *testing.T) {
	r := newRig(t, parse(t, "", 100), at(2, 0))
	r.tick()
	want := r.ramp()
	for i := 0; i < divergencesToWarn; i++ {
		r.g.Overwrite(aoc, gamma.Identity(gammatest.Size))
		r.clk.Add(gammaVerifyInterval + time.Second)
		r.tick()
		if !r.ramp().Equal(want) {
			t.Fatalf("round %d: ramp was not reapplied", i)
		}
	}
	warned := false
	for _, w := range r.c.Status().Warnings {
		if strings.Contains(w, "another program") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected a warning after %d divergences: %v", divergencesToWarn, r.c.Status().Warnings)
	}
}

func TestMonitorsChangedEventForcesAReapply(t *testing.T) {
	r := newRig(t, parse(t, "", 100), at(2, 0))
	r.tick()
	want := r.ramp()
	r.g.Overwrite(aoc, gamma.Identity(gammatest.Size)) // for example after a resume
	r.c.rescanNow.Store(true)
	r.tick()
	if !r.ramp().Equal(want) {
		t.Fatal("the event must reapply the ramp without waiting for the next check")
	}
}

func TestGammaFailureBacksOffAndRecovers(t *testing.T) {
	r := newRig(t, parse(t, "", 100), at(2, 0))
	r.g.FailSet[0] = errors.New("D-Bus hiccup")
	for i := 0; i < failuresBeforeOffline; i++ {
		r.tick()
		r.clk.Add(maxBackoff + time.Second)
	}
	st := r.c.Status().Monitors[0]
	if st.Online || st.Fails < failuresBeforeOffline || !strings.Contains(st.LastError, "hiccup") {
		t.Fatalf("status = %+v", st)
	}
	delete(r.g.FailSet, 0)
	r.tick()
	if st := r.c.Status().Monitors[0]; !st.Online || st.LastError != "" {
		t.Fatalf("did not recover: %+v", st)
	}
	if _, _, blue := end(r.ramp()); !near(blue, 65535*0.30) {
		t.Fatalf("blue = %d", blue)
	}
}

func TestNoBackendWarnsAndRetries(t *testing.T) {
	r := newRig(t, parse(t, "", 100), at(2, 0))
	r.g.OutsErr = gamma.ErrNoBackend
	r.tick()
	st := r.c.Status()
	if len(st.Monitors) != 0 || len(st.Warnings) == 0 || !strings.Contains(st.Warnings[0], "no software color backend") {
		t.Fatalf("status = %+v", st)
	}
	r.g.OutsErr = nil
	r.clk.Add(offlineRescanInterval + time.Second)
	r.tick()
	if len(r.c.Status().Monitors) != 1 {
		t.Fatal("the controller must pick the screens up once the backend is back")
	}
}

func TestNilBackendDoesNotCrash(t *testing.T) {
	c := New(Options{Config: parse(t, "", 100), Now: func() time.Time { return at(2, 0) }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	c.Tick(context.Background())
	c.Shutdown()
	if len(c.Status().Warnings) == 0 {
		t.Fatal("expected a warning")
	}
}

func TestConfigReloadChangesTheTarget(t *testing.T) {
	r := newRig(t, parse(t, "floor = 0", 100), at(12, 0))
	r.tick()
	r.c.SetConfig(parse(t, "floor = 0", 50))
	r.tick()
	if red, _, _ := end(r.ramp()); !near(red, 65535*0.50) {
		t.Fatalf("red = %d", red)
	}
}

func ddcRig(t *testing.T, target, start int) (*rig, *ddctest.FakeDisplay) {
	t.Helper()
	r := newRig(t, parse(t, `brightness = "ddc"`, target), at(12, 0))
	d := ddctest.NewFakeDisplay(ddc.Info{Model: "24G4", Serial: "SN1", Connector: "card1-DP-1", Bus: 5})
	d.SetValue(ddc.VCPBrightness, start)
	r.displs = []ddc.Display{d}
	return r, d
}

func TestDDCBrightnessWritesOnlyTheBacklightAndRestoresIt(t *testing.T) {
	r, d := ddcRig(t, 53, 50)
	r.tick()
	if r.finds != 1 {
		t.Fatalf("Discover calls = %d", r.finds)
	}
	writes := d.Writes()
	if len(writes) != 3 {
		t.Fatalf("writes = %+v", writes)
	}
	for _, w := range writes {
		if w.VCP != ddc.VCPBrightness {
			t.Fatalf("wrote %s; only 0x10 is allowed", w.VCP)
		}
	}
	if d.Value(ddc.VCPBrightness) != 53 {
		t.Fatalf("brightness = %d", d.Value(ddc.VCPBrightness))
	}
	if st := r.c.Status().Monitors[0]; st.Current != 53 || st.Scale != 100 || st.Backend != "ddc" || st.Bus != 5 || !st.DDC {
		t.Fatalf("status = %+v", st)
	}
	if !r.ramp().Equal(gamma.Identity(gammatest.Size)) {
		t.Fatal("with DDC brightness the ramp carries only color (identity at noon)")
	}
	r.c.Shutdown()
	if d.Value(ddc.VCPBrightness) != 50 {
		t.Fatalf("shutdown must restore the original brightness 50, got %d", d.Value(ddc.VCPBrightness))
	}
	for _, w := range d.Writes() {
		if w.VCP != ddc.VCPBrightness {
			t.Fatalf("wrote %s", w.VCP)
		}
	}
}

func TestDDCBrightnessWithoutADisplayIsReported(t *testing.T) {
	r := newRig(t, parse(t, `brightness = "ddc"`, 50), at(12, 0))
	r.tick()
	st := r.c.Status().Monitors[0]
	if st.Online || !strings.Contains(st.LastError, "no DDC") {
		t.Fatalf("status = %+v", st)
	}
}

func TestResumeFadesBackInGradually(t *testing.T) {
	r := newRig(t, parse(t, "", 100), at(2, 0))
	r.tick()
	r.c.Pause()
	r.tick()
	before := len(r.g.History(0))
	r.c.Resume()
	r.tick()
	hist := r.g.History(0)[before:]
	if len(hist) < 10 {
		t.Fatalf("resume wrote %d ramps; it should fade in step by step", len(hist))
	}
	if _, _, blue := end(hist[0]); blue < 65535-700 {
		t.Fatalf("the first ramp after resume jumped straight to blue = %d", blue)
	}
}
