package controller

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dusk/internal/config"
	"dusk/internal/ddc"
	"dusk/internal/ddc/ddctest"
	"dusk/internal/nightlight"
	"dusk/internal/state"
)

// ---- test infrastructure (nothing here touches real hardware or gsettings) ----

type clock struct {
	mu     sync.Mutex
	t      time.Time
	sleeps []time.Duration
}

func (k *clock) Now() time.Time {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.t
}

func (k *clock) set(t time.Time) {
	k.mu.Lock()
	k.t = t
	k.mu.Unlock()
}

func (k *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	k.mu.Lock()
	k.t = k.t.Add(d)
	k.sleeps = append(k.sleeps, d)
	k.mu.Unlock()
	return nil
}

func (k *clock) Sleeps() []time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]time.Duration(nil), k.sleeps...)
}

type fakeNight struct {
	mu       sync.Mutex
	orig     nightlight.Original
	enables  []int
	restores []nightlight.Original
	snaps    int
}

func (f *fakeNight) Snapshot(context.Context) (nightlight.Original, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps++
	return f.orig, nil
}

func (f *fakeNight) Enable(_ context.Context, k int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enables = append(f.enables, k)
	return nil
}

func (f *fakeNight) Restore(_ context.Context, o nightlight.Original) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restores = append(f.restores, o)
	return nil
}

var (
	day   = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC) // intensity 0
	night = time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)  // plateau, intensity 1
)

type env struct {
	ctl      *Controller
	clk      *clock
	store    *state.Store
	night    *fakeNight
	logs     *bytes.Buffer
	mu       sync.Mutex
	displays []ddc.Display
}

func nightCfg(fallback string) config.Night {
	return config.Night{Enabled: true, Start: 21 * 60, End: 7 * 60, Ramp: 30 * time.Minute,
		BlueGain: 60, RedGain: 100, GreenGain: 100, Fallback: fallback}
}

func constMon(match string, b int) config.Monitor {
	return config.Monitor{Name: match, Match: match, Points: []config.Point{{Minute: 0, Brightness: b}}}
}

func newDisp(model string, bus int, brightness int) *ddctest.FakeDisplay {
	d := ddctest.NewFakeDisplay(ddc.Info{Model: model, Serial: "S", Bus: bus})
	d.SetValue(ddc.VCPBrightness, brightness)
	return d
}

func noRGB(d *ddctest.FakeDisplay) *ddctest.FakeDisplay {
	d.DisplayCaps.RGB = false
	return d
}

func newEnv(t *testing.T, n config.Night, start time.Time, mons []config.Monitor, displays ...ddc.Display) *env {
	t.Helper()
	cfg := &config.Config{PollInterval: 30 * time.Second, RescanInterval: 5 * time.Minute, Night: n, Monitors: mons}
	e := &env{
		clk:      &clock{t: start},
		store:    &state.Store{Path: filepath.Join(t.TempDir(), "state.json")},
		night:    &fakeNight{orig: nightlight.Original{Enabled: false, Automatic: true, From: 20, To: 6, Temperature: 2700}},
		logs:     &bytes.Buffer{},
		displays: displays,
	}
	e.ctl = New(Options{
		Config: cfg,
		Discover: func(context.Context) ([]ddc.Display, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return append([]ddc.Display(nil), e.displays...), nil
		},
		Night: e.night,
		Store: e.store,
		Now:   e.clk.Now,
		Sleep: e.clk.Sleep,
		Log:   slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	return e
}

func (e *env) setDisplays(ds ...ddc.Display) {
	e.mu.Lock()
	e.displays = ds
	e.mu.Unlock()
}

func (e *env) tick() { e.ctl.Tick(context.Background()) }

func (e *env) saved(t *testing.T) state.State {
	t.Helper()
	st, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func values(ws []ddctest.Write, vcp ddc.VCP) []int {
	var out []int
	for _, w := range ws {
		if w.VCP == vcp {
			out = append(out, w.Value)
		}
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const keyS = "M#S"

// ---- brightness ----

func TestBrightnessConvergesInStepsOfOne(t *testing.T) {
	d := newDisp("M", 1, 30)
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("M", 33)}, d)
	e.tick()
	if got := values(d.Writes(), ddc.VCPBrightness); !equalInts(got, []int{31, 32, 33}) {
		t.Errorf("writes = %v", got)
	}
	for _, s := range e.clk.Sleeps() {
		if s < 2*time.Second {
			t.Errorf("wait %v < 2 s between writes", s)
		}
	}
	if n := len(e.clk.Sleeps()); n != 3 {
		t.Errorf("waits = %d; want 3", n)
	}
}

func TestNoWritesWhenAtTarget(t *testing.T) {
	d := newDisp("M", 1, 50)
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("M", 50)}, d)
	e.tick()
	e.tick()
	if len(d.Writes()) != 0 || len(e.clk.Sleeps()) != 0 {
		t.Errorf("no difference must not write: %v", d.Writes())
	}
}

func TestManualChangeIsReapplied(t *testing.T) {
	d := newDisp("M", 1, 40)
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("M", 40)}, d)
	e.tick()
	d.SetValue(ddc.VCPBrightness, 37) // someone used the monitor buttons
	e.tick()
	if got := values(d.Writes(), ddc.VCPBrightness); !equalInts(got, []int{38, 39, 40}) {
		t.Errorf("reapplied writes = %v", got)
	}
}

func TestUnmatchedMonitorIsNeverWritten(t *testing.T) {
	d := newDisp("OUTRO", 1, 10)
	e := newEnv(t, nightCfg("auto"), night, []config.Monitor{constMon("M", 90)}, d)
	e.tick()
	if len(d.Writes()) != 0 {
		t.Errorf("a monitor without a schedule received writes: %v", d.Writes())
	}
	if len(e.night.enables) != 0 {
		t.Error("a monitor without a schedule must not trigger the fallback")
	}
}

// ---- night mode through RGB ----

func TestNightRGBSavesOriginalBeforeFirstWrite(t *testing.T) {
	d := newDisp("M", 1, 60)
	var savedAtFirstWrite state.RGB
	var seen bool
	var e *env
	d.OnSet = func(vcp ddc.VCP, _ int) {
		if vcp != ddc.VCPBlueGain || seen {
			return
		}
		seen = true
		st, err := e.store.Load()
		if err != nil {
			t.Errorf("reading the state at the first write: %v", err)
		}
		savedAtFirstWrite = st.Originals[keyS]
	}
	e = newEnv(t, nightCfg("off"), night, []config.Monitor{constMon("M", 60)}, d)
	e.tick()

	if !seen {
		t.Fatal("no blue write happened")
	}
	if savedAtFirstWrite != (state.RGB{R: 100, G: 100, B: 100}) {
		t.Errorf("original missing on disk at the first write: %+v", savedAtFirstWrite)
	}
	ws := d.Writes()
	if len(values(ws, ddc.VCPRedGain)) != 0 || len(values(ws, ddc.VCPGreenGain)) != 0 {
		t.Error("red and green must stay untouched")
	}
	blue := values(ws, ddc.VCPBlueGain)
	if len(blue) != 40 || blue[0] != 99 || blue[39] != 60 {
		t.Errorf("blue = %v", blue)
	}
	for i := 1; i < len(blue); i++ {
		if blue[i-1]-blue[i] != 1 {
			t.Fatalf("step != 1 at %d: %v", i, blue)
		}
	}
	if len(values(ws, ddc.VCPBrightness)) != 0 {
		t.Error("brightness was already at the target")
	}
}

func TestNightRestoresAtDayAndClearsOriginal(t *testing.T) {
	d := newDisp("M", 1, 60)
	d.SetValue(ddc.VCPBlueGain, 60)
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("M", 60)}, d)
	st := state.New()
	st.Originals[keyS] = state.RGB{R: 100, G: 100, B: 100}
	if err := e.store.Save(st); err != nil {
		t.Fatal(err)
	}
	e.tick()
	if d.Value(ddc.VCPBlueGain) != 100 {
		t.Errorf("blue = %d; want 100", d.Value(ddc.VCPBlueGain))
	}
	if _, ok := e.saved(t).Originals[keyS]; ok {
		t.Error("the original should be deleted once restored")
	}
}

func TestRestartMidNightKeepsOriginal(t *testing.T) {
	d := newDisp("M", 1, 60)
	d.SetValue(ddc.VCPBlueGain, 80) // halfway through the ramp when the daemon died
	e := newEnv(t, nightCfg("off"), night, []config.Monitor{constMon("M", 60)}, d)
	st := state.New()
	st.Originals[keyS] = state.RGB{R: 100, G: 100, B: 100}
	if err := e.store.Save(st); err != nil {
		t.Fatal(err)
	}
	e.tick()
	if d.Value(ddc.VCPBlueGain) != 60 {
		t.Errorf("blue = %d", d.Value(ddc.VCPBlueGain))
	}
	if got := e.saved(t).Originals[keyS]; got != (state.RGB{R: 100, G: 100, B: 100}) {
		t.Errorf("original overwritten with the ramp value: %+v", got)
	}
}

func TestRGBWriteIsRefusedIfOriginalCannotBeSaved(t *testing.T) {
	d := newDisp("M", 1, 60)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, nightCfg("off"), night, []config.Monitor{constMon("M", 60)}, d)
	e.ctl.store = &state.Store{Path: filepath.Join(blocker, "state.json")} // parent is a file: Save fails
	e.tick()
	if len(d.Writes()) != 0 {
		t.Errorf("must not change the monitor without saving the original: %v", d.Writes())
	}
}

// ---- night-light fallback ----

func TestFallbackForMonitorWithoutRGB(t *testing.T) {
	a := newDisp("A", 1, 60)
	b := noRGB(newDisp("B", 2, 60))
	e := newEnv(t, nightCfg("auto"), night, []config.Monitor{constMon("A", 60), constMon("B", 60)}, a, b)
	e.tick()

	if len(e.night.enables) != 1 || e.night.enables[0] != 2700 {
		t.Errorf("Enable = %v; want [2700] (the user's own temperature)", e.night.enables)
	}
	if e.saved(t).NightLight == nil || e.saved(t).NightLight.Temperature != 2700 {
		t.Errorf("night-light original was not saved: %+v", e.saved(t).NightLight)
	}
	if got := a.Value(ddc.VCPBlueGain); got != 80 { // half the intensity: 100 - 0.5*40
		t.Errorf("A blue = %d; want 80 (A-8)", got)
	}
	if len(b.Writes()) != 0 {
		t.Errorf("B has no RGB and must receive no writes: %v", b.Writes())
	}

	// Dawn: restores the night light and the RGB.
	e.clk.set(day)
	e.tick()
	if len(e.night.restores) != 1 || e.night.restores[0] != e.night.orig {
		t.Errorf("Restore = %+v", e.night.restores)
	}
	if e.saved(t).NightLight != nil {
		t.Error("the night-light state should be cleared")
	}
	if a.Value(ddc.VCPBlueGain) != 100 {
		t.Errorf("A blue = %d", a.Value(ddc.VCPBlueGain))
	}
	if e.night.snaps != 1 {
		t.Errorf("snapshots = %d; want 1", e.night.snaps)
	}
}

func TestFallbackOffNeverTouchesNightLight(t *testing.T) {
	b := noRGB(newDisp("B", 2, 60))
	e := newEnv(t, nightCfg("off"), night, []config.Monitor{constMon("B", 60)}, b)
	e.tick()
	if e.night.snaps+len(e.night.enables)+len(e.night.restores) != 0 {
		t.Error("with fallback=off the night light must not be touched")
	}
}

func TestNoFallbackWhenAllMonitorsHaveRGB(t *testing.T) {
	a := newDisp("A", 1, 60)
	e := newEnv(t, nightCfg("auto"), night, []config.Monitor{constMon("A", 60)}, a)
	e.tick()
	if len(e.night.enables) != 0 {
		t.Error("the fallback must not turn on when every monitor has RGB")
	}
	if a.Value(ddc.VCPBlueGain) != 60 {
		t.Errorf("without the fallback the intensity is full: blue = %d", a.Value(ddc.VCPBlueGain))
	}
}

func TestFallbackWhenRGBWriteFails(t *testing.T) {
	a := newDisp("A", 1, 60)
	a.SetFailure(errors.New("monitor ignores RGB"))
	e := newEnv(t, nightCfg("auto"), night, []config.Monitor{constMon("A", 60)}, a)
	e.tick() // RGB write fails: marks rgbBroken
	e.clk.set(night.Add(time.Hour))
	e.tick() // backoff still applies, but the fallback is already decided from state
	if len(e.night.enables) == 0 {
		t.Error("with broken RGB the fallback should turn on")
	}
}

// ---- failures, backoff, rediscovery ----

func TestFailureIsolationAndBackoff(t *testing.T) {
	a := newDisp("A", 1, 10)
	b := newDisp("B", 2, 10)
	a.SetFailure(errors.New("i2c hung"))
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("A", 20), constMon("B", 11)}, a, b)
	e.tick()
	if got := values(b.Writes(), ddc.VCPBrightness); !equalInts(got, []int{11}) {
		t.Errorf("B should converge even while A fails: %v", got)
	}
	if a.SetAttempts() != 1 {
		t.Fatalf("attempts on A = %d", a.SetAttempts())
	}
	e.tick() // right after: A is in backoff
	if a.SetAttempts() != 1 {
		t.Errorf("A was tried during backoff: %d", a.SetAttempts())
	}
	a.SetFailure(nil)
	e.clk.set(e.clk.Now().Add(time.Minute))
	e.tick()
	if a.Value(ddc.VCPBrightness) != 20 {
		t.Errorf("A should recover: %d", a.Value(ddc.VCPBrightness))
	}
	if !strings.Contains(e.logs.String(), "monitor failure") {
		t.Error("the failure should appear in the logs")
	}
}

func TestBackoffGrowsUpToFiveMinutes(t *testing.T) {
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second}
	for i, w := range want {
		if got := backoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %v; want %v", i+1, got, w)
		}
	}
	if backoff(50) != 5*time.Minute {
		t.Errorf("cap = %v", backoff(50))
	}
}

func TestRescanPicksUpNewMonitor(t *testing.T) {
	a := newDisp("A", 1, 10)
	b := newDisp("B", 2, 10)
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("A", 10), constMon("B", 12)}, a)
	e.tick()
	if len(b.Writes()) != 0 {
		t.Fatal("B does not exist yet")
	}
	e.setDisplays(a, b)
	e.clk.set(e.clk.Now().Add(6 * time.Minute))
	e.tick()
	if got := values(b.Writes(), ddc.VCPBrightness); !equalInts(got, []int{11, 12}) {
		t.Errorf("B after rediscovery = %v", got)
	}
	if !strings.Contains(e.logs.String(), "monitor found") {
		t.Error("discovery log missing")
	}
}

func TestOfflineMonitorTriggersRescanWithinAMinute(t *testing.T) {
	sleeping := newDisp("A", 1, 10)
	sleeping.DisplayCaps = ddc.Caps{} // did not respond during discovery
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("A", 12)}, sleeping)
	e.tick()
	awake := newDisp("A", 1, 10)
	e.setDisplays(awake)
	e.clk.set(e.clk.Now().Add(61 * time.Second)) // well before the 5 min
	e.tick()
	if got := values(awake.Writes(), ddc.VCPBrightness); !equalInts(got, []int{11, 12}) {
		t.Errorf("monitor that woke up = %v", got)
	}
}

func TestDiscoverFailureKeepsCurrentMonitors(t *testing.T) {
	d := newDisp("A", 1, 10)
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("A", 11)}, d)
	e.tick()
	e.ctl.discover = func(context.Context) ([]ddc.Display, error) { return nil, errors.New("no ddcutil") }
	d.SetValue(ddc.VCPBrightness, 5)
	e.clk.set(e.clk.Now().Add(10 * time.Minute))
	e.tick()
	if d.Value(ddc.VCPBrightness) != 11 {
		t.Errorf("should keep controlling the known monitor: %d", d.Value(ddc.VCPBrightness))
	}
}

// ---- shutdown and Run ----

func TestShutdownRestoresRGBAtOnceAndNightLight(t *testing.T) {
	a := newDisp("A", 1, 60)
	b := noRGB(newDisp("B", 2, 60))
	e := newEnv(t, nightCfg("auto"), night, []config.Monitor{constMon("A", 60), constMon("B", 60)}, a, b)
	e.tick()
	before := len(a.Writes())
	sleeps := len(e.clk.Sleeps())

	e.ctl.Shutdown()

	ws := a.Writes()[before:]
	if len(ws) != 3 {
		t.Fatalf("restoring should be one write per channel: %v", ws)
	}
	for _, w := range ws {
		if w.Value != 100 {
			t.Errorf("write %v did not restore the original", w)
		}
	}
	if len(e.clk.Sleeps()) != sleeps {
		t.Error("restoring on shutdown must not wait between writes")
	}
	if len(e.night.restores) != 1 {
		t.Errorf("night light not restored: %v", e.night.restores)
	}
	st := e.saved(t)
	if len(st.Originals) != 0 || st.NightLight != nil {
		t.Errorf("state should be clean: %+v", st)
	}
	if a.Value(ddc.VCPBrightness) != 60 {
		t.Error("brightness must not be reverted")
	}
}

func TestRunStopsOnCancelAndShutsDown(t *testing.T) {
	d := newDisp("M", 1, 60)
	e := newEnv(t, nightCfg("off"), night, []config.Monitor{constMon("M", 60)}, d)
	ctx, cancel := context.WithCancel(context.Background())
	polls := 0
	e.ctl.sleep = func(ctx context.Context, dur time.Duration) error {
		if dur == 30*time.Second {
			polls++
			if polls == 2 {
				cancel()
			}
		}
		return e.clk.Sleep(ctx, dur)
	}
	done := make(chan error, 1)
	go func() { done <- e.ctl.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not finish")
	}
	if polls != 2 {
		t.Errorf("polls = %d", polls)
	}
	if d.Value(ddc.VCPBlueGain) != 100 {
		t.Errorf("on shutdown blue should return to 100, it is %d", d.Value(ddc.VCPBlueGain))
	}
}

func TestSetConfigTakesEffectNextTick(t *testing.T) {
	d := newDisp("M", 1, 10)
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("M", 10)}, d)
	e.tick()
	next := *e.ctl.cfg.Load()
	next.Monitors = []config.Monitor{constMon("M", 12)}
	e.ctl.SetConfig(&next)
	e.tick()
	if d.Value(ddc.VCPBrightness) != 12 {
		t.Errorf("brightness = %d", d.Value(ddc.VCPBrightness))
	}
}

// ---- logs (NFR-5) ----

func TestLogsRelevantEvents(t *testing.T) {
	d := newDisp("M", 1, 60)
	e := newEnv(t, nightCfg("off"), night, []config.Monitor{constMon("M", 61)}, d)
	e.tick()
	for _, want := range []string{"monitor found", "night mode", "write", "monitor=M#S"} {
		if !strings.Contains(e.logs.String(), want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

func TestAutoFallbackWithoutBackendWarnsOnceAndUsesDDCOnly(t *testing.T) {
	a := newDisp("A", 1, 60)
	b := noRGB(newDisp("B", 2, 60))
	e := newEnv(t, nightCfg("auto"), night, []config.Monitor{constMon("A", 60), constMon("B", 60)}, a, b)
	e.ctl.night = nil // no backend detected on this machine
	e.tick()
	e.tick()
	if got := a.Value(ddc.VCPBlueGain); got != 60 {
		t.Errorf("without a backend A keeps the full DDC intensity: blue = %d", got)
	}
	if n := strings.Count(e.logs.String(), "no night-light backend available"); n != 1 {
		t.Errorf("warning count = %d; want 1", n)
	}
}

// ---- pause, wake, status ----

func TestPauseFreezesWritesAndResumeContinues(t *testing.T) {
	d := newDisp("M", 1, 10)
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("M", 12)}, d)
	e.ctl.Pause()
	e.tick()
	if len(d.Writes()) != 0 || !e.ctl.Paused() {
		t.Fatalf("a paused controller must not write: %v", d.Writes())
	}
	e.ctl.Resume()
	e.tick()
	if got := values(d.Writes(), ddc.VCPBrightness); !equalInts(got, []int{11, 12}) {
		t.Errorf("after resume = %v (should walk to the target in single steps)", got)
	}
}

func TestPauseInterruptsAConvergenceInProgress(t *testing.T) {
	d := newDisp("M", 1, 10)
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("M", 60)}, d)
	writes := 0
	e.ctl.sleep = func(ctx context.Context, dur time.Duration) error {
		writes++
		if writes == 3 {
			e.ctl.Pause()
		}
		return e.clk.Sleep(ctx, dur)
	}
	e.tick()
	if got := len(d.Writes()); got != 3 {
		t.Errorf("writes before the pause took effect = %d; want 3", got)
	}
}

func TestStatusSnapshot(t *testing.T) {
	a := newDisp("A", 1, 20)
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("A", 22)}, a, newDisp("UNCONFIGURED", 2, 5))
	if st := e.ctl.Status(); len(st.Monitors) != 0 || st.Paused {
		t.Errorf("before the first tick: %+v", st)
	}
	e.tick()
	st := e.ctl.Status()
	if len(st.Monitors) != 1 {
		t.Fatalf("only monitors with a schedule are listed: %+v", st.Monitors)
	}
	m := st.Monitors[0]
	if m.Name != "A" || m.Key != keyA || m.Bus != 1 || m.Current != 22 || m.Target != 22 || !m.Online || m.Fails != 0 || m.LastError != "" {
		t.Errorf("monitor status = %+v", m)
	}
	e.ctl.Pause()
	if !e.ctl.Status().Paused {
		t.Error("Status must reflect the pause immediately")
	}
}

const keyA = "A#S"

func TestStatusShowsFailures(t *testing.T) {
	a := newDisp("A", 1, 20)
	a.SetFailure(errors.New("i2c hung"))
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("A", 25)}, a)
	e.tick()
	m := e.ctl.Status().Monitors[0]
	if m.Fails != 1 || !strings.Contains(m.LastError, "i2c hung") {
		t.Errorf("status after a failure = %+v", m)
	}
}

func TestWakeShortensTheWait(t *testing.T) {
	d := newDisp("M", 1, 40)
	e := newEnv(t, nightCfg("off"), day, []config.Monitor{constMon("M", 40)}, d)
	waiting := make(chan struct{}, 10)
	e.ctl.sleep = func(ctx context.Context, dur time.Duration) error {
		if dur == 30*time.Second {
			waiting <- struct{}{}
			<-ctx.Done() // a real poll wait: only a wake or a cancel ends it
			return ctx.Err()
		}
		return e.clk.Sleep(ctx, dur)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.ctl.Run(ctx); close(done) }()

	expect := func(what string) {
		t.Helper()
		select {
		case <-waiting:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
		}
	}
	expect("the first poll wait")
	d.SetValue(ddc.VCPBrightness, 35) // a manual change
	e.ctl.Wake()
	expect("the second poll wait (after Wake)")
	if d.Value(ddc.VCPBrightness) != 40 {
		t.Errorf("Wake should trigger an immediate tick that corrects the value: %d", d.Value(ddc.VCPBrightness))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- per-monitor color levels ----

func TestEachMonitorUsesItsOwnNightColorLevel(t *testing.T) {
	a := newDisp("A", 1, 60)
	b := newDisp("B", 2, 60)
	blue := 40
	monA := constMon("A", 60)
	monA.Night = config.MonitorNight{BlueGain: &blue}
	e := newEnv(t, nightCfg("off"), night, []config.Monitor{monA, constMon("B", 60)}, a, b)
	e.tick()
	if got := a.Value(ddc.VCPBlueGain); got != 40 {
		t.Errorf("A has its own level: blue = %d; want 40", got)
	}
	if got := b.Value(ddc.VCPBlueGain); got != 60 {
		t.Errorf("B follows the global level: blue = %d; want 60", got)
	}
	// At dawn both return to their saved originals regardless of the level they used.
	e.clk.set(day)
	e.tick()
	if a.Value(ddc.VCPBlueGain) != 100 || b.Value(ddc.VCPBlueGain) != 100 {
		t.Errorf("originals not restored: %d %d", a.Value(ddc.VCPBlueGain), b.Value(ddc.VCPBlueGain))
	}
}

func TestPerMonitorLevelAppliesToRedAndGreenToo(t *testing.T) {
	a := newDisp("A", 1, 60)
	red, green := 80, 90
	mon := constMon("A", 60)
	mon.Night = config.MonitorNight{RedGain: &red, GreenGain: &green}
	e := newEnv(t, nightCfg("off"), night, []config.Monitor{mon}, a)
	e.tick()
	if a.Value(ddc.VCPRedGain) != 80 || a.Value(ddc.VCPGreenGain) != 90 || a.Value(ddc.VCPBlueGain) != 60 {
		t.Errorf("rgb = %d %d %d; want 80 90 60", a.Value(ddc.VCPRedGain), a.Value(ddc.VCPGreenGain), a.Value(ddc.VCPBlueGain))
	}
}

func TestReloadedColorLevelTakesEffect(t *testing.T) {
	a := newDisp("A", 1, 60)
	e := newEnv(t, nightCfg("off"), night, []config.Monitor{constMon("A", 60)}, a)
	e.tick()
	if a.Value(ddc.VCPBlueGain) != 60 {
		t.Fatalf("blue = %d", a.Value(ddc.VCPBlueGain))
	}
	warmer := 30
	next := *e.ctl.cfg.Load()
	mon := constMon("A", 60)
	mon.Night = config.MonitorNight{BlueGain: &warmer}
	next.Monitors = []config.Monitor{mon}
	e.ctl.SetConfig(&next)
	e.tick()
	if got := a.Value(ddc.VCPBlueGain); got != 30 {
		t.Errorf("after the reload blue = %d; want 30 (reached gradually, one point per write)", got)
	}
}
