// Package controller applies the brightness schedule and night mode to the monitors.
package controller

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"dusk/internal/config"
	"dusk/internal/ddc"
	"dusk/internal/monitors"
	"dusk/internal/nightlight"
	"dusk/internal/schedule"
	"dusk/internal/state"
)

const (
	defaultMinWriteInterval = 2 * time.Second
	maxWritesPerTick        = 300
	offlineRescanInterval   = time.Minute
	failuresBeforeOffline   = 3
	baseBackoff             = 5 * time.Second
	maxBackoff              = 5 * time.Minute
	nightLightMinDelta      = 10 // K
	shutdownTimeout         = 15 * time.Second
	// fallbackFactor halves the DDC intensity when the night light also acts (A-8).
	fallbackFactor = 0.5
)

var rgbVCPs = [3]ddc.VCP{ddc.VCPRedGain, ddc.VCPGreenGain, ddc.VCPBlueGain}

// Options holds the Controller's dependencies.
type Options struct {
	Config   *config.Config
	Discover func(ctx context.Context) ([]ddc.Display, error)
	Night    nightlight.Backend // may be nil: no night-light fallback
	Store    *state.Store       // may be nil: state kept in memory only
	Now      func() time.Time
	Sleep    func(ctx context.Context, d time.Duration) error
	Log      *slog.Logger

	MinWriteInterval time.Duration // default 2 s
}

type monState struct {
	disp      ddc.Display
	fails     int
	until     time.Time
	lastErr   string
	rgbBroken bool
	curB      int    // last brightness read or written
	curRGB    [3]int // last red, green and blue gains read or written
	rgbKnown  bool
	// known is true while curB (and curRGB) reflect what the hardware reported or what dusk wrote;
	// verifiedAt is when the monitor was last read back.
	known      bool
	verifiedAt time.Time
}

// Controller applies the schedule. Tick, Run and Shutdown must not run concurrently.
type Controller struct {
	discover func(ctx context.Context) ([]ddc.Display, error)
	night    nightlight.Backend
	store    *state.Store
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
	log      *slog.Logger
	minWrite time.Duration

	cfg atomic.Pointer[config.Config]

	mu sync.Mutex // guards st
	st state.State

	loaded          bool
	scanned         bool
	displays        []ddc.Display
	mons            map[string]*monState
	lastRescan      time.Time
	nlActive        bool
	nlKelvin        int
	nightActive     bool
	warnedNoBackend bool

	paused      atomic.Bool
	forceVerify atomic.Bool // read every monitor again at the next tick
	kick        chan struct{}
	statusMu    sync.Mutex
	status      Status
}

// RGB holds red, green and blue values.
type RGB struct{ R, G, B int }

// MonitorStatus is a snapshot of one configured monitor.
type MonitorStatus struct {
	Name      string
	Key       string
	Bus       int
	Current   int
	Target    int
	Online    bool
	Fails     int
	LastError string

	ColorControl bool
	// RGB is the last color gain read from the monitor (nil if unknown or unsupported).
	RGB *RGB
	// RGBTarget is where night mode is taking the gains right now (nil when dusk is not touching them).
	RGBTarget *RGB
	// RGBOriginal is the gain saved before dusk changed anything (nil when nothing was changed).
	RGBOriginal *RGB
	// NightLevel is the configured gain, in percent of the original, at the peak of night mode.
	NightLevel RGB
	// OwnLevel is true when the monitor overrides the global level.
	OwnLevel bool
	Schedule []config.Point
}

// NightStatus describes night mode in the running configuration.
type NightStatus struct {
	Enabled        bool
	Start, End     int // minutes since 00:00
	Ramp           time.Duration
	Phase          schedule.Phase
	NextChange     time.Time
	Fallback       string
	FallbackActive bool
	FallbackKelvin int
}

// Status is a snapshot of the controller, refreshed at the end of every Tick.
type Status struct {
	Paused         bool
	Night          float64 // night-mode intensity, 0 to 1
	NightMode      NightStatus
	PollInterval   time.Duration
	RescanInterval time.Duration
	VerifyInterval time.Duration
	Monitors       []MonitorStatus
}

// New creates the Controller.
func New(o Options) *Controller {
	c := &Controller{
		discover: o.Discover,
		night:    o.Night,
		store:    o.Store,
		now:      o.Now,
		sleep:    o.Sleep,
		log:      o.Log,
		minWrite: o.MinWriteInterval,
		mons:     map[string]*monState{},
		kick:     make(chan struct{}, 1),
		st:       state.New(),
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.sleep == nil {
		c.sleep = realSleep
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	if c.minWrite <= 0 {
		c.minWrite = defaultMinWriteInterval
	}
	c.cfg.Store(o.Config)
	return c
}

func realSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// SetConfig swaps the configuration; the next Tick already uses it.
func (c *Controller) SetConfig(cfg *config.Config) { c.cfg.Store(cfg) }

// Run calls Tick every poll_interval until the context is canceled, then
// calls Shutdown.
func (c *Controller) Run(ctx context.Context) error {
	for {
		c.Tick(ctx)
		if err := c.wait(ctx, c.cfg.Load().PollInterval); err != nil {
			break
		}
	}
	c.Shutdown()
	return nil
}

// Pause freezes all writes: brightness and RGB stay where they are until Resume.
func (c *Controller) Pause() {
	if !c.paused.Swap(true) {
		c.log.Info("paused")
	}
}

// Resume undoes Pause and wakes the loop so the monitors head to their targets now.
func (c *Controller) Resume() {
	if c.paused.Swap(false) {
		c.log.Info("resumed")
	}
	c.forceVerify.Store(true) // someone may have used the monitor buttons while paused
	c.Wake()
}

// Paused reports whether the controller is paused.
func (c *Controller) Paused() bool { return c.paused.Load() }

// Wake makes Run start its next Tick immediately instead of waiting out the poll interval.
func (c *Controller) Wake() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Status returns the latest snapshot.
func (c *Controller) Status() Status {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	st := c.status
	st.Monitors = append([]MonitorStatus(nil), st.Monitors...)
	st.Paused = c.paused.Load()
	return st
}

// wait sleeps for d, or less if Wake is called. It returns an error only when ctx is done.
func (c *Controller) wait(ctx context.Context, d time.Duration) error {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-c.kick:
			cancel()
		case <-wctx.Done():
		}
	}()
	_ = c.sleep(wctx, d)
	return ctx.Err()
}

// Tick does one pass: it rediscovers monitors if needed, decides on the
// night-light fallback and converges every monitor in parallel.
func (c *Controller) Tick(ctx context.Context) {
	if c.paused.Load() {
		return
	}
	if c.forceVerify.Swap(false) {
		for _, m := range c.mons {
			m.known = false
		}
	}
	cfg := c.cfg.Load()
	c.ensureLoaded()
	now := c.now()
	c.maybeRescan(ctx, now, cfg)

	intensity := schedule.NightIntensity(cfg.Night, now)
	if (intensity > 0) != c.nightActive {
		c.nightActive = intensity > 0
		c.log.Info("night mode", "active", c.nightActive)
	}

	type job struct {
		m   *monState
		mon *config.Monitor
	}
	var jobs []job
	for _, a := range monitors.Match(cfg, c.displays) {
		if a.Monitor == nil {
			continue
		}
		if m := c.mons[a.Display.Info().Key()]; m != nil {
			jobs = append(jobs, job{m, a.Monitor})
		}
	}

	needFallback := false
	if cfg.Night.Fallback == config.FallbackAuto && cfg.Night.Enabled && intensity > 0 {
		for _, j := range jobs {
			caps := j.m.disp.Caps()
			if caps.Brightness && (!caps.RGB || j.m.rgbBroken) {
				needFallback = true
				break
			}
		}
	}
	factor := 1.0
	if c.updateNightLight(ctx, needFallback, intensity) {
		factor = fallbackFactor
	}

	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.converge(ctx, j.m, j.mon, cfg, factor)
		}()
	}
	wg.Wait()

	st := Status{Night: intensity, PollInterval: cfg.PollInterval, RescanInterval: cfg.RescanInterval, VerifyInterval: verifyInterval(cfg)}
	phase, next := schedule.NightPhase(cfg.Night, now)
	st.NightMode = NightStatus{
		Enabled: cfg.Night.Enabled, Start: cfg.Night.Start, End: cfg.Night.End, Ramp: cfg.Night.Ramp,
		Phase: phase, NextChange: next, Fallback: cfg.Night.Fallback,
		FallbackActive: c.nlActive, FallbackKelvin: c.nlKelvin,
	}
	for _, j := range jobs {
		target, _ := schedule.Target(*j.mon, now)
		info := j.m.disp.Info()
		caps := j.m.disp.Caps()
		nightM := j.mon.NightFor(cfg.Night)
		ms := MonitorStatus{
			Name:         j.mon.Name,
			Key:          info.Key(),
			Bus:          info.Bus,
			Current:      j.m.curB,
			Target:       target,
			Online:       caps.Brightness && j.m.fails < failuresBeforeOffline,
			Fails:        j.m.fails,
			LastError:    j.m.lastErr,
			ColorControl: caps.RGB,
			NightLevel:   RGB{R: nightM.RedGain, G: nightM.GreenGain, B: nightM.BlueGain},
			OwnLevel:     !j.mon.Night.IsZero(),
			Schedule:     append([]config.Point(nil), j.mon.Points...),
		}
		if caps.RGB {
			if j.m.rgbKnown {
				cur := RGB{R: j.m.curRGB[0], G: j.m.curRGB[1], B: j.m.curRGB[2]}
				ms.RGB = &cur
			}
			orig, hasOrig := c.original(info.Key())
			if hasOrig {
				ms.RGBOriginal = &RGB{R: orig.R, G: orig.G, B: orig.B}
			}
			if eff := intensity * factor; eff > 0 || hasOrig {
				g0, g1, g2 := schedule.NightGains(nightM, eff)
				base := orig
				if !hasOrig && j.m.rgbKnown {
					base = state.RGB{R: j.m.curRGB[0], G: j.m.curRGB[1], B: j.m.curRGB[2]}
				}
				ms.RGBTarget = &RGB{
					R: int(math.Round(float64(base.R) * float64(g0) / 100)),
					G: int(math.Round(float64(base.G) * float64(g1) / 100)),
					B: int(math.Round(float64(base.B) * float64(g2) / 100)),
				}
			}
		}
		st.Monitors = append(st.Monitors, ms)
	}
	c.statusMu.Lock()
	c.status = st
	c.statusMu.Unlock()
}

// updateNightLight turns the night light on, adjusts it or restores it, and
// reports whether the fallback is active.
func (c *Controller) updateNightLight(ctx context.Context, need bool, intensity float64) bool {
	if c.night == nil {
		if need && !c.warnedNoBackend {
			c.warnedNoBackend = true
			c.log.Warn("no night-light backend available; night mode uses DDC only")
		}
		return false
	}
	c.mu.Lock()
	owned := c.st.NightLight
	c.mu.Unlock()

	if !need {
		if owned != nil {
			if err := c.night.Restore(ctx, *owned); err != nil {
				c.log.Warn("restoring night light", "error", err)
				return false
			}
			c.mu.Lock()
			c.st.NightLight = nil
			c.saveLocked()
			c.mu.Unlock()
			c.nlActive = false
			c.log.Info("night light restored")
		}
		return false
	}

	if owned == nil {
		snap, err := c.night.Snapshot(ctx)
		if err != nil {
			c.log.Warn("reading night light; fallback postponed", "error", err)
			return false
		}
		c.mu.Lock()
		c.st.NightLight = &snap
		c.saveLocked()
		c.mu.Unlock()
		owned = &snap
	}
	kelvin := schedule.NightLightKelvin(intensity, owned.Temperature)
	if !c.nlActive || abs(kelvin-c.nlKelvin) >= nightLightMinDelta {
		if err := c.night.Enable(ctx, kelvin); err != nil {
			c.log.Warn("enabling night light", "error", err)
			return false
		}
		if !c.nlActive {
			c.log.Info("night light fallback on", "kelvin", kelvin)
		}
		c.nlActive, c.nlKelvin = true, kelvin
	}
	return true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (c *Controller) ensureLoaded() {
	if c.loaded {
		return
	}
	c.loaded = true
	if c.store == nil {
		return
	}
	st, err := c.store.Load()
	if err != nil {
		c.log.Warn("unreadable state; starting empty", "error", err)
	}
	c.mu.Lock()
	c.st = st
	c.mu.Unlock()
}

// saveLocked writes the state; the caller must hold c.mu.
func (c *Controller) saveLocked() {
	if c.store == nil {
		return
	}
	if err := c.store.Save(c.st); err != nil {
		c.log.Warn("saving state", "error", err)
	}
}

func (c *Controller) original(key string) (state.RGB, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.st.Originals[key]
	return v, ok
}

// setOriginal records the original and returns only after saving to disk.
func (c *Controller) setOriginal(key string, v state.RGB) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.Originals[key] = v
	if c.store == nil {
		return nil
	}
	return c.store.Save(c.st)
}

func (c *Controller) delOriginal(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.st.Originals, key)
	c.saveLocked()
}

func (c *Controller) maybeRescan(ctx context.Context, now time.Time, cfg *config.Config) {
	due := !c.scanned
	if c.scanned {
		offline := len(c.displays) == 0
		for _, m := range c.mons {
			caps := m.disp.Caps()
			if !caps.Brightness || m.fails >= failuresBeforeOffline {
				offline = true
			}
		}
		since := now.Sub(c.lastRescan)
		due = since >= cfg.RescanInterval || (offline && since >= offlineRescanInterval)
	}
	if !due {
		return
	}
	c.scanned = true
	c.lastRescan = now
	ds, err := c.discover(ctx)
	if err != nil {
		c.log.Warn("monitor rediscovery failed; keeping the current list", "error", err)
		return
	}
	mons := map[string]*monState{}
	var list []ddc.Display
	for _, d := range ds {
		key := d.Info().Key()
		if _, dup := mons[key]; dup {
			c.log.Warn("monitor with duplicate identity ignored", "monitor", key, "bus", d.Info().Bus)
			continue
		}
		m := c.mons[key]
		if m == nil {
			m = &monState{}
			c.log.Info("monitor found", "monitor", key, "bus", d.Info().Bus,
				"brightness", d.Caps().Brightness, "rgb", d.Caps().RGB)
		}
		m.disp = d
		if d.Caps().Brightness && m.fails >= failuresBeforeOffline {
			m.fails, m.until, m.lastErr = 0, time.Time{}, ""
			m.known = false
		}
		mons[key] = m
		list = append(list, d)
	}
	for key := range c.mons {
		if _, ok := mons[key]; !ok {
			c.log.Info("monitor gone", "monitor", key)
		}
	}
	c.mons, c.displays = mons, list
}

// verifyInterval returns how often monitors are read back, with the default applied.
func verifyInterval(cfg *config.Config) time.Duration {
	if cfg.VerifyInterval <= 0 {
		return config.DefaultVerifyInterval
	}
	return cfg.VerifyInterval
}

func backoff(fails int) time.Duration {
	d := baseBackoff
	for i := 1; i < fails && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}

func (c *Controller) fail(m *monState, err error) {
	m.fails++
	m.known = false // do not trust cached values after a failure
	m.lastErr = err.Error()
	wait := backoff(m.fails)
	m.until = c.now().Add(wait)
	c.log.Warn("monitor failure", "monitor", m.disp.Info().Key(), "error", err,
		"failures", m.fails, "retry_in", wait)
}

// converge moves the monitor to its target in steps of one point, waiting
// between writes.
func (c *Controller) converge(ctx context.Context, m *monState, mon *config.Monitor, cfg *config.Config, factor float64) {
	info := m.disp.Info()
	key := info.Key()
	now := c.now()
	if now.Before(m.until) {
		return
	}
	caps := m.disp.Caps()
	if !caps.Brightness {
		m.lastErr = "no response during discovery"
		return
	}
	// Reading a monitor is a slow I2C transaction that can stall the GPU driver for an instant, so the
	// hardware is only read when a check is due (or after a failure). In between, the values dusk
	// already knows are used, and a tick with nothing to change touches the hardware not at all.
	due := !m.known || now.Sub(m.verifiedAt) >= verifyInterval(cfg)
	if due {
		v, _, err := m.disp.Get(ctx, ddc.VCPBrightness)
		if err != nil {
			c.fail(m, err)
			return
		}
		m.curB = v
	}
	curB := m.curB

	nightM := mon.NightFor(cfg.Night) // this monitor's own color gains
	eff := schedule.NightIntensity(cfg.Night, now) * factor
	orig, hasOrig := c.original(key)
	manageRGB := caps.RGB && (eff > 0 || hasOrig)
	// The gains are read when a check is due, when none is known yet, and always right before the
	// originals are saved (the saved value must be what the monitor really has).
	if caps.RGB && (due || !m.rgbKnown || (manageRGB && !hasOrig)) {
		var v [3]int
		ok := true
		for i, vcp := range rgbVCPs {
			x, _, err := m.disp.Get(ctx, vcp)
			if err != nil {
				if manageRGB {
					c.fail(m, err)
					return
				}
				ok = false // only for `dusk status`: not worth failing over
				break
			}
			v[i] = x
		}
		if ok {
			m.curRGB, m.rgbKnown = v, true
		}
	}
	curRGB := m.curRGB
	if manageRGB && !hasOrig {
		orig = state.RGB{R: curRGB[0], G: curRGB[1], B: curRGB[2]}
		if err := c.setOriginal(key, orig); err != nil {
			c.log.Warn("could not save the original RGB; leaving the monitor untouched", "monitor", key, "error", err)
			m.lastErr = err.Error()
			return
		}
		hasOrig = true
	}
	if due {
		m.known, m.verifiedAt = true, now
	}
	origRGB := [3]int{orig.R, orig.G, orig.B}

	for i := 0; i < maxWritesPerTick; i++ {
		if ctx.Err() != nil || c.paused.Load() {
			return
		}
		now = c.now()
		var vcp ddc.VCP
		var cur, target int
		write := false

		if tb, ok := schedule.Target(*mon, now); ok && tb != curB {
			vcp, cur, target, write = ddc.VCPBrightness, curB, tb, true
		} else if manageRGB {
			eff = schedule.NightIntensity(cfg.Night, now) * factor
			g0, g1, g2 := schedule.NightGains(nightM, eff)
			gains := [3]int{g0, g1, g2}
			for ch := range rgbVCPs {
				t := int(math.Round(float64(origRGB[ch]) * float64(gains[ch]) / 100))
				if curRGB[ch] != t {
					vcp, cur, target, write = rgbVCPs[ch], curRGB[ch], t, true
					break
				}
			}
		}
		if !write {
			break
		}
		next := schedule.Step(cur, target, 1)
		if err := m.disp.Set(ctx, vcp, next); err != nil {
			if vcp != ddc.VCPBrightness {
				m.rgbBroken = true
			}
			c.fail(m, err)
			return
		}
		c.log.Debug("write", "monitor", key, "vcp", vcp.String(), "value", next, "target", target)
		if vcp == ddc.VCPBrightness {
			curB = next
			m.curB = next
		} else {
			m.rgbBroken = false
			for ch := range rgbVCPs {
				if rgbVCPs[ch] == vcp {
					curRGB[ch] = next
					m.curRGB = curRGB
				}
			}
		}
		if err := c.sleep(ctx, c.minWrite); err != nil {
			return
		}
	}

	if manageRGB && hasOrig && curRGB == origRGB && schedule.NightIntensity(cfg.Night, c.now())*factor == 0 {
		c.delOriginal(key)
		c.log.Info("original RGB restored", "monitor", key)
	}
	m.fails, m.lastErr = 0, ""
}

// Shutdown restores the monitors' original RGB at once, and the night light.
// Brightness is not reverted. It uses its own context because Run's is
// already canceled.
func (c *Controller) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	c.ensureLoaded()
	for _, d := range c.displays {
		key := d.Info().Key()
		orig, ok := c.original(key)
		if !ok || !d.Caps().RGB {
			continue
		}
		vals := [3]int{orig.R, orig.G, orig.B}
		failed := false
		for i, vcp := range rgbVCPs {
			if err := d.Set(ctx, vcp, vals[i]); err != nil {
				c.log.Warn("restoring RGB on shutdown", "monitor", key, "error", err)
				failed = true
				break
			}
		}
		if !failed {
			c.delOriginal(key)
			c.log.Info("original RGB restored on shutdown", "monitor", key)
		}
	}
	c.mu.Lock()
	owned := c.st.NightLight
	c.mu.Unlock()
	if owned != nil && c.night != nil {
		if err := c.night.Restore(ctx, *owned); err != nil {
			c.log.Warn("restoring night light on shutdown", "error", err)
			return
		}
		c.mu.Lock()
		c.st.NightLight = nil
		c.saveLocked()
		c.mu.Unlock()
		c.nlActive = false
	}
}
