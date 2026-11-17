// Filename: internal/controller/controller.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

// Package controller applies the brightness schedule and night mode to the
// screens. Color and brightness are software gamma ramps by default; a monitor
// can opt in to the DDC backlight (VCP 0x10 only).
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"dusk/internal/config"
	"dusk/internal/ddc"
	"dusk/internal/gamma"
	"dusk/internal/monitors"
	"dusk/internal/schedule"
)

const (
	defaultMinWriteInterval = 2 * time.Second // between DDC writes
	defaultStepDelay        = 40 * time.Millisecond
	maxStepsPerTick         = 300
	offlineRescanInterval   = time.Minute
	failuresBeforeOffline   = 3
	baseBackoff             = 5 * time.Second
	maxBackoff              = 5 * time.Minute
	shutdownTimeout         = 15 * time.Second
	// gammaVerifyInterval is how often the applied ramp is read back to notice
	// that another program replaced it (reading a ramp is cheap).
	gammaVerifyInterval = time.Minute
	divergenceWindow    = 10 * time.Minute
	divergencesToWarn   = 3
)

// Options holds the Controller's dependencies.
type Options struct {
	Config   *config.Config
	Gamma    gamma.Backend
	Discover func(ctx context.Context) ([]ddc.Display, error) // nil: no DDC at all
	Now      func() time.Time
	Sleep    func(ctx context.Context, d time.Duration) error
	Log      *slog.Logger

	MinWriteInterval time.Duration // between DDC writes, default 2 s
	StepDelay        time.Duration // between gamma steps, default 40 ms
}

type monState struct {
	res monitors.Resolved

	// Software state: gains and brightness actually applied, in percent.
	cur      [3]int
	curSW    int
	size     int // ramp entries per channel, 0 until read
	applied  gamma.Ramp
	hasApply bool // applied is what the screen should hold right now
	checked  time.Time
	diverged []time.Time

	// DDC brightness state (backend "ddc" only).
	curB     int
	known    bool
	ddcCheck time.Time
	origB    int
	hasOrigB bool

	fails   int
	until   time.Time
	lastErr string
}

// Controller applies the schedule. Tick, Run and Shutdown must not run concurrently.
type Controller struct {
	gamma    gamma.Backend
	discover func(ctx context.Context) ([]ddc.Display, error)
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
	log      *slog.Logger
	minWrite time.Duration
	stepWait time.Duration

	cfg atomic.Pointer[config.Config]

	scanned     bool
	ddcScanned  bool
	lastRescan  time.Time
	outputs     []gamma.Output
	displays    []ddc.Display
	backendErr  string
	mons        map[string]*monState
	nightActive bool

	paused         atomic.Bool
	pausedRestored bool
	rescanNow      atomic.Bool // monitors changed (hotplug, resume): rescan and reapply
	kick           chan struct{}
	statusMu       sync.Mutex
	status         Status
}

// RGB holds red, green and blue values.
type RGB struct{ R, G, B int }

// MonitorStatus is a snapshot of one configured monitor.
type MonitorStatus struct {
	Name   string
	Key    string
	Output string // connector name, for example "DP-1"
	Bus    int    // DDC bus, 0 when the monitor has no DDC
	DDC    bool

	Backend string // config.BrightnessSoftware or config.BrightnessDDC
	Floor   int
	Current int // brightness applied, percent
	Target  int

	// Gamma is the color gain applied now, in percent per channel; Scale the brightness scale in percent.
	Gamma RGB
	Scale int

	Online    bool
	Fails     int
	LastError string

	// NightLevel is the configured gain, in percent, at the peak of night mode.
	NightLevel RGB
	OwnLevel   bool // true when the monitor overrides the global level
	Schedule   []config.Point
}

// NightStatus describes night mode in the running configuration.
type NightStatus struct {
	Enabled    bool
	Start, End int // minutes since 00:00
	Ramp       time.Duration
	Phase      schedule.Phase
	NextChange time.Time
}

// Status is a snapshot of the controller, refreshed at the end of every Tick.
type Status struct {
	Paused         bool
	Night          float64 // night-mode intensity, 0 to 1
	NightMode      NightStatus
	PollInterval   time.Duration
	RescanInterval time.Duration
	VerifyInterval time.Duration
	Warnings       []string
	Monitors       []MonitorStatus
}

// New creates the Controller.
func New(o Options) *Controller {
	c := &Controller{
		gamma:    o.Gamma,
		discover: o.Discover,
		now:      o.Now,
		sleep:    o.Sleep,
		log:      o.Log,
		minWrite: o.MinWriteInterval,
		stepWait: o.StepDelay,
		mons:     map[string]*monState{},
		kick:     make(chan struct{}, 1),
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
	if c.stepWait <= 0 {
		c.stepWait = defaultStepDelay
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
// calls Shutdown. It also reacts to monitor hotplug and resume events.
func (c *Controller) Run(ctx context.Context) error {
	if c.gamma != nil {
		ev := make(chan struct{}, 1)
		go func() {
			if err := c.gamma.Watch(ctx, ev); err != nil {
				c.log.Debug("monitor change events unavailable", "error", err)
			}
		}()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ev:
					c.rescanNow.Store(true)
					c.Wake()
				}
			}
		}()
	}
	for {
		c.Tick(ctx)
		if err := c.wait(ctx, c.cfg.Load().PollInterval); err != nil {
			break
		}
	}
	c.Shutdown()
	return nil
}

// Pause puts the screens back to their normal colors and freezes all changes until Resume.
func (c *Controller) Pause() {
	if !c.paused.Swap(true) {
		c.pausedRestored = false
		c.log.Info("paused")
		c.Wake()
	}
}

// Resume undoes Pause and wakes the loop so the screens head to their targets now.
func (c *Controller) Resume() {
	if c.paused.Swap(false) {
		c.log.Info("resumed")
	}
	c.rescanNow.Store(true)
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
	st.Warnings = append([]string(nil), st.Warnings...)
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

func needsDDC(cfg *config.Config) bool {
	for _, m := range cfg.Monitors {
		if m.Brightness == config.BrightnessDDC {
			return true
		}
	}
	return false
}

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
	m.known = false
	m.hasApply = false // do not trust what is on the screen after a failure
	m.lastErr = err.Error()
	wait := backoff(m.fails)
	m.until = c.now().Add(wait)
	c.log.Warn("monitor failure", "monitor", m.res.Output.Name, "error", err, "failures", m.fails, "retry_in", wait)
}

// maybeRescan refreshes the list of screens (and DDC displays when a monitor needs them).
func (c *Controller) maybeRescan(ctx context.Context, now time.Time, cfg *config.Config) {
	event := c.rescanNow.Swap(false)
	wantDDC := needsDDC(cfg) && c.discover != nil
	due := !c.scanned || event || (wantDDC && !c.ddcScanned)
	if !due {
		offline := c.backendErr != ""
		for _, m := range c.mons {
			if m.fails >= failuresBeforeOffline {
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
	if c.gamma != nil {
		outs, err := c.gamma.Outputs(ctx)
		if err != nil {
			if c.backendErr == "" {
				c.log.Warn("cannot list the screens; will retry", "error", err)
			}
			c.backendErr = err.Error()
		} else {
			c.backendErr = ""
			c.outputs = outs
		}
	} else {
		c.backendErr = "no software color backend"
	}
	if wantDDC {
		c.ddcScanned = true
		ds, err := c.discover(ctx)
		if err != nil {
			c.log.Warn("DDC rediscovery failed; keeping the current list", "error", err)
		} else {
			c.displays = ds
		}
	} else if !needsDDC(cfg) {
		c.displays, c.ddcScanned = nil, false
	}
	if event {
		for _, m := range c.mons {
			m.hasApply = false // another program may have reset the ramps
			m.until, m.fails, m.lastErr = time.Time{}, 0, ""
		}
	}
}

// Tick does one pass: it rescans if due and converges every configured screen in parallel.
func (c *Controller) Tick(ctx context.Context) {
	cfg := c.cfg.Load()
	now := c.now()
	if c.paused.Load() {
		c.restoreWhilePaused(ctx)
		return
	}
	c.maybeRescan(ctx, now, cfg)

	intensity := schedule.NightIntensity(cfg.Night, now)
	if (intensity > 0) != c.nightActive {
		c.nightActive = intensity > 0
		c.log.Info("night mode", "active", c.nightActive)
	}

	var jobs []*monState
	seen := map[string]bool{}
	for _, r := range monitors.Resolve(cfg, c.outputs, c.displays) {
		if r.Monitor == nil {
			continue
		}
		name := r.Output.Name
		seen[name] = true
		m := c.mons[name]
		if m == nil {
			m = &monState{cur: [3]int{100, 100, 100}, curSW: 100}
			c.mons[name] = m
			c.log.Info("screen found", "screen", name, "monitor", r.Monitor.Name, "ddc", r.Display != nil)
		}
		m.res = r
		jobs = append(jobs, m)
	}
	for name := range c.mons {
		if !seen[name] {
			delete(c.mons, name)
			c.log.Info("screen gone", "screen", name)
		}
	}

	var wg sync.WaitGroup
	for _, m := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.converge(ctx, m, cfg)
		}()
	}
	wg.Wait()
	c.publish(cfg, now, intensity, jobs)
}

func (c *Controller) publish(cfg *config.Config, now time.Time, intensity float64, jobs []*monState) {
	st := Status{Night: intensity, PollInterval: cfg.PollInterval, RescanInterval: cfg.RescanInterval, VerifyInterval: verifyInterval(cfg)}
	phase, next := schedule.NightPhase(cfg.Night, now)
	st.NightMode = NightStatus{Enabled: cfg.Night.Enabled, Start: cfg.Night.Start, End: cfg.Night.End, Ramp: cfg.Night.Ramp, Phase: phase, NextChange: next}
	st.Warnings = append(st.Warnings, cfg.Warnings...)
	if c.backendErr != "" {
		st.Warnings = append(st.Warnings, "no software color backend: "+c.backendErr)
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].res.Output.Name < jobs[b].res.Output.Name })
	for _, m := range jobs {
		mon := m.res.Monitor
		target, _ := schedule.Target(*mon, now)
		nightM := mon.NightFor(cfg.Night)
		useDDC := mon.Brightness == config.BrightnessDDC && m.res.Display != nil
		ms := MonitorStatus{
			Name: mon.Name, Key: m.res.Output.Key(), Output: m.res.Output.Name,
			DDC: m.res.Display != nil, Backend: mon.Brightness, Floor: mon.Floor,
			Current: m.curSW, Target: target,
			Gamma:  RGB{R: m.cur[0], G: m.cur[1], B: m.cur[2]},
			Scale:  int(gamma.BrightnessScale(m.curSW, mon.Floor)*100 + 0.5),
			Online: m.fails < failuresBeforeOffline, Fails: m.fails, LastError: m.lastErr,
			NightLevel: RGB{R: nightM.RedGain, G: nightM.GreenGain, B: nightM.BlueGain},
			OwnLevel:   !mon.Night.IsZero(),
			Schedule:   append([]config.Point(nil), mon.Points...),
		}
		if m.res.Display != nil {
			ms.Bus = m.res.Display.Info().Bus
		}
		if useDDC {
			ms.Current = m.curB
			ms.Scale = 100
		}
		if mon.Brightness == config.BrightnessDDC && m.res.Display == nil {
			ms.Online, ms.LastError = false, "brightness = \"ddc\" but the monitor has no DDC/CI"
		}
		if len(m.diverged) >= divergencesToWarn {
			st.Warnings = append(st.Warnings, fmt.Sprintf("another program keeps changing the colors of %s (is GNOME Night Light on?)", m.res.Output.Name))
		}
		st.Monitors = append(st.Monitors, ms)
	}
	c.statusMu.Lock()
	c.status = st
	c.statusMu.Unlock()
}

// restoreWhilePaused puts every touched screen back to the identity ramp, once per pause.
func (c *Controller) restoreWhilePaused(ctx context.Context) {
	if c.pausedRestored {
		return
	}
	c.pausedRestored = true
	for _, m := range c.mons {
		if m.size == 0 {
			continue
		}
		if err := c.gamma.Set(ctx, m.res.Output, gamma.Identity(m.size)); err != nil {
			c.log.Warn("restoring colors on pause", "screen", m.res.Output.Name, "error", err)
		}
		m.cur, m.curSW, m.hasApply = [3]int{100, 100, 100}, 100, false
	}
}

func (c *Controller) applyRamp(ctx context.Context, m *monState, floor int) error {
	r := gamma.Build(m.size, float64(m.cur[0])/100, float64(m.cur[1])/100, float64(m.cur[2])/100, gamma.BrightnessScale(m.curSW, floor))
	if err := c.gamma.Set(ctx, m.res.Output, r); err != nil {
		return err
	}
	m.applied, m.hasApply = r, true
	return nil
}

// converge moves one screen to its target: it verifies the ramp, handles the DDC
// brightness when asked, and steps the gamma toward the goal.
func (c *Controller) converge(ctx context.Context, m *monState, cfg *config.Config) {
	mon := m.res.Monitor
	now := c.now()
	if now.Before(m.until) || c.gamma == nil || c.backendErr != "" {
		return
	}
	if m.size == 0 {
		r, err := c.gamma.Get(ctx, m.res.Output)
		if err != nil {
			c.fail(m, err)
			return
		}
		m.size = r.Len()
	}
	if m.hasApply && now.Sub(m.checked) >= gammaVerifyInterval {
		got, err := c.gamma.Get(ctx, m.res.Output)
		if err != nil {
			c.fail(m, err)
			return
		}
		m.checked = now
		if !got.Equal(m.applied) {
			m.hasApply = false
			m.diverged = append(pruneBefore(m.diverged, now.Add(-divergenceWindow)), now)
			c.log.Warn("the screen ramp was changed by another program; reapplying", "screen", m.res.Output.Name)
		}
	}

	useDDC := mon.Brightness == config.BrightnessDDC && m.res.Display != nil
	nightM := mon.NightFor(cfg.Night)
	r, g, b := schedule.NightGains(nightM, schedule.NightIntensity(cfg.Night, now))
	goal := [3]int{r, g, b}
	tb, _ := schedule.Target(*mon, now)
	goalSW := tb
	if useDDC {
		goalSW = 100
		if !c.convergeDDC(ctx, m, tb, cfg, now) {
			return
		}
	}

	if !m.hasApply {
		if err := c.applyRamp(ctx, m, mon.Floor); err != nil {
			c.fail(m, err)
			return
		}
		m.checked = now
	}
	for i := 0; i < maxStepsPerTick; i++ {
		if ctx.Err() != nil || c.paused.Load() {
			return
		}
		next := m.cur
		for ch := range next {
			next[ch] = schedule.Step(m.cur[ch], goal[ch], 1)
		}
		nextSW := schedule.Step(m.curSW, goalSW, 1)
		if next == m.cur && nextSW == m.curSW {
			break
		}
		m.cur, m.curSW = next, nextSW
		if err := c.applyRamp(ctx, m, mon.Floor); err != nil {
			c.fail(m, err)
			return
		}
		if err := c.sleep(ctx, c.stepWait); err != nil {
			return
		}
	}
	m.fails, m.lastErr = 0, ""
}

func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}

// convergeDDC moves the monitor's own backlight to the target in steps of one
// point. It writes only VCP 0x10. It reports false when the monitor failed.
func (c *Controller) convergeDDC(ctx context.Context, m *monState, target int, cfg *config.Config, now time.Time) bool {
	d := m.res.Display
	if !m.known || now.Sub(m.ddcCheck) >= verifyInterval(cfg) {
		v, _, err := d.Get(ctx, ddc.VCPBrightness)
		if err != nil {
			c.fail(m, err)
			return false
		}
		m.curB, m.known, m.ddcCheck = v, true, now
		if !m.hasOrigB {
			m.origB, m.hasOrigB = v, true
		}
	}
	for i := 0; i < maxStepsPerTick && m.curB != target; i++ {
		if ctx.Err() != nil || c.paused.Load() {
			return true
		}
		next := schedule.Step(m.curB, target, 1)
		if err := d.Set(ctx, ddc.VCPBrightness, next); err != nil {
			c.fail(m, err)
			return false
		}
		m.curB = next
		if err := c.sleep(ctx, c.minWrite); err != nil {
			return true
		}
	}
	return true
}

// Shutdown puts every touched screen back to the identity ramp and restores the
// DDC brightness read at the start. It uses its own context because Run's is already canceled.
func (c *Controller) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for name, m := range c.mons {
		if c.gamma != nil && m.size > 0 {
			if err := c.gamma.Set(ctx, m.res.Output, gamma.Identity(m.size)); err != nil {
				c.log.Warn("restoring colors on shutdown", "screen", name, "error", err)
			} else {
				c.log.Info("colors restored on shutdown", "screen", name)
			}
		}
		if m.res.Display != nil && m.res.Monitor != nil && m.res.Monitor.Brightness == config.BrightnessDDC && m.hasOrigB {
			if err := m.res.Display.Set(ctx, ddc.VCPBrightness, m.origB); err != nil {
				c.log.Warn("restoring brightness on shutdown", "screen", name, "error", err)
			}
		}
	}
}
