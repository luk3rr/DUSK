// Package setup is the interactive `dusk setup` wizard. It detects the
// monitors, asks the user how each should behave and writes the TOML file.
// It only reads from monitors and from the desktop; it never writes to them.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"dusk/internal/config"
	"dusk/internal/ddc"
	"dusk/internal/monitors"
	"dusk/internal/preflight"
	"dusk/internal/prompt"
)

// Errors returned by Run.
var (
	ErrPreflight  = errors.New("this computer is not ready for dusk (see the failed checks above)")
	ErrNoMonitors = errors.New("no DDC/CI monitors were found")
	ErrDeclined   = errors.New("nothing was written: the configuration was not confirmed")
)

// Options are the wizard's inputs. Everything that touches the machine is
// injectable.
type Options struct {
	ConfigPath string
	Yes        bool // accept every default without asking
	DDC        ddc.Runner
	Gsettings  ddc.Runner // nil means no night-light backend
	FS         preflight.FS
	In         io.Reader
	Out        io.Writer
	Now        func() time.Time
	// Prompter, when set, is used instead of one built from In and Out. It lets
	// a caller (dusk install) share one input stream with the wizard.
	Prompter *prompt.Prompter
}

// entry is one distinct monitor identity (several identical monitors without
// a serial number share one).
type entry struct {
	info     ddc.Info
	count    int
	rgb      bool
	bright   bool
	current  int
	existing *config.Monitor
}

type point = config.Point

// Run executes the wizard.
func Run(ctx context.Context, o Options) error {
	if o.Now == nil {
		o.Now = time.Now
	}
	out := o.Out
	p := o.Prompter
	if p == nil {
		p = prompt.New(o.In, out, o.Yes)
	}

	p.Say("dusk setup: checking this computer, then detecting your monitors. Nothing is changed on them.\n")
	rep := preflight.Check(ctx, preflight.Env{DDC: o.DDC, Gsettings: o.Gsettings, FS: o.FS})
	rep.Print(out)
	if rep.Failed() {
		return ErrPreflight
	}

	displays, err := monitors.Discover(ctx, o.DDC)
	if err != nil {
		return err
	}
	if len(displays) == 0 {
		p.Say("\nNo DDC/CI monitors were found.")
		p.Say("Check that DDC/CI is enabled in each monitor's on-screen menu, that the cable or dock passes DDC,")
		p.Say("and that `ddcutil detect` lists your monitors.")
		return ErrNoMonitors
	}

	existing, err := loadExisting(o.ConfigPath)
	if err != nil {
		p.Say("\nThe existing configuration cannot be used:\n%v\n", err)
		ok, cerr := p.Confirm("Start from scratch? (the old file is kept as a backup)", true)
		if cerr != nil {
			return cerr
		}
		if !ok {
			return ErrDeclined
		}
		existing = nil
	}

	entries := groupEntries(ctx, p, displays, existing)
	printMonitors(out, entries)

	used := map[string]bool{}
	for _, e := range entries {
		if e.existing != nil {
			used[e.existing.Name] = true
		}
	}

	cfg := &config.Config{PollInterval: config.DefaultPollInterval, RescanInterval: config.DefaultRescanInterval, VerifyInterval: config.DefaultVerifyInterval}
	if existing != nil {
		cfg.PollInterval, cfg.RescanInterval, cfg.VerifyInterval = existing.PollInterval, existing.RescanInterval, existing.VerifyInterval
	}
	notes := map[string]string{}
	for _, e := range entries {
		m, err := askMonitor(p, e, used)
		if err != nil {
			return err
		}
		cfg.Monitors = append(cfg.Monitors, m)
		notes[m.Name] = describe(e)
	}

	// Keep entries for monitors that are not connected right now.
	if existing != nil {
		matched := map[*config.Monitor]bool{}
		for _, a := range monitors.Match(existing, displays) {
			if a.Monitor != nil {
				matched[a.Monitor] = true
			}
		}
		var kept []string
		for i := range existing.Monitors {
			if !matched[&existing.Monitors[i]] {
				cfg.Monitors = append(cfg.Monitors, existing.Monitors[i])
				kept = append(kept, existing.Monitors[i].Name)
			}
		}
		if len(kept) > 0 {
			p.Say("\nKeeping the entries for monitors that are not connected now: %s", strings.Join(kept, ", "))
		}
	}

	cfg.Night, err = askNight(p, cfg, existing, entries, rep.NightLight)
	if err != nil {
		return err
	}
	if err := askColorLevels(p, cfg, entries); err != nil {
		return err
	}

	data := config.Marshal(cfg, notes)
	if _, err := config.Parse(data); err != nil {
		return fmt.Errorf("internal error: the generated configuration is invalid: %w", err)
	}
	p.Say("\nThis is the configuration that will be written to %s:\n", o.ConfigPath)
	p.Say("----------------------------------------")
	fmt.Fprint(out, string(data))
	p.Say("----------------------------------------")
	ok, err := p.Confirm("Write it?", true)
	if err != nil {
		return err
	}
	if !ok {
		return ErrDeclined
	}

	backup, err := writeFile(o.ConfigPath, data, o.Now())
	if err != nil {
		return err
	}
	p.Say("\nWrote %s (%d monitor(s), night mode %s).", o.ConfigPath, len(cfg.Monitors), onOff(cfg.Night.Enabled))
	if backup != "" {
		p.Say("The previous file was saved as %s.", backup)
	}
	p.Say("\nNext steps:")
	p.Say("  dusk check-config   validate the file")
	p.Say("  dusk run            start dusk now, in this terminal")
	p.Say("  dusk install        start it automatically at every login (systemd user service)")
	p.Say("  dusk status         see what it is doing")
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// loadExisting returns nil, nil when there is no file.
func loadExisting(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return cfg, err
}

// groupEntries collapses displays that share an identity into one entry.
func groupEntries(ctx context.Context, p *prompt.Prompter, displays []ddc.Display, existing *config.Config) []*entry {
	byKey := map[string]*entry{}
	var order []*entry
	assigned := map[ddc.Display]*config.Monitor{}
	for _, a := range monitors.Match(existing, displays) {
		assigned[a.Display] = a.Monitor
	}
	for _, d := range displays {
		info, caps := d.Info(), d.Caps()
		key := info.Key()
		e := byKey[key]
		if e == nil {
			e = &entry{info: info, rgb: caps.RGB, bright: caps.Brightness, current: 50, existing: assigned[d]}
			if caps.Brightness {
				if cur, _, err := d.Get(ctx, ddc.VCPBrightness); err == nil {
					e.current = cur
				}
			}
			byKey[key] = e
			order = append(order, e)
		}
		e.count++
		e.rgb = e.rgb && caps.RGB
	}
	for _, e := range order {
		if e.count > 1 {
			p.Say("Note: %d monitors report the same identity %q; they will share one entry and one schedule.", e.count, e.info.Key())
		}
	}
	return order
}

func printMonitors(out io.Writer, entries []*entry) {
	fmt.Fprintln(out, "\nDetected monitors:")
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  MODEL\tSERIAL\tPORT\tBRIGHTNESS\tCOLOR CONTROL")
	for _, e := range entries {
		serial := e.info.Serial
		if serial == "" {
			serial = "-"
		}
		bright := "no response"
		if e.bright {
			bright = strconv.Itoa(e.current) + "%"
		}
		rgb := "no"
		if e.rgb {
			rgb = "yes"
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", e.info.Model, serial, e.info.Connector, bright, rgb)
	}
	w.Flush()
	fmt.Fprintln(out)
}

func describe(e *entry) string {
	serial := "no serial"
	if e.info.Serial != "" {
		serial = "serial " + e.info.Serial
	}
	return fmt.Sprintf("%s %s (%s), %s, i2c bus %d", e.info.Mfg, e.info.Model, serial, e.info.Connector, e.info.Bus)
}

var presetPoints = []struct {
	label  string
	clock  string
	bright int
}{
	{"daytime", "08:00", 70},
	{"evening", "18:00", 50},
	{"night", "22:00", 25},
}

func askMonitor(p *prompt.Prompter, e *entry, used map[string]bool) (config.Monitor, error) {
	p.Say("Monitor: %s", describe(e))
	var carry config.MonitorNight // a per-monitor color level survives whatever is chosen below
	if e.existing != nil {
		carry = e.existing.Night
	}
	defName := slug(e.info.Model)
	if e.existing != nil {
		defName = e.existing.Name
	}
	own := ""
	if e.existing != nil {
		own = e.existing.Name
	}
	defName = uniqueName(defName, used, own)
	name, err := p.String("Name for this monitor", defName, func(v string) error {
		switch {
		case v == "":
			return errors.New("the name cannot be empty")
		case v != strings.TrimSpace(v):
			return errors.New("the name cannot start or end with spaces")
		case used[v] && v != own:
			return fmt.Errorf("the name %q is already used", v)
		}
		return nil
	})
	if err != nil {
		return config.Monitor{}, err
	}
	used[name] = true

	options := []string{"Day / evening / night preset (three brightness levels)", "One constant brightness", "Custom key points"}
	def := 0
	if e.existing != nil {
		options = append([]string{"Keep the current schedule"}, options...)
	}
	choice, err := p.Choice("Brightness schedule:", options, def)
	if err != nil {
		return config.Monitor{}, err
	}
	if e.existing != nil {
		if choice == 0 {
			return config.Monitor{Name: name, Match: e.info.Key(), Points: e.existing.Points, Night: carry}, nil
		}
		choice--
	}
	var pts []point
	switch choice {
	case 0:
		for _, pp := range presetPoints {
			clock, err := p.Clock(fmt.Sprintf("Time the %s level starts", pp.label), pp.clock)
			if err != nil {
				return config.Monitor{}, err
			}
			b, err := p.Int(fmt.Sprintf("Brightness for %s", pp.label), pp.bright, 0, 100)
			if err != nil {
				return config.Monitor{}, err
			}
			minute, _ := config.ParseClock(clock)
			pts = append(pts, point{Minute: minute, Brightness: b})
		}
	case 1:
		b, err := p.Int("Brightness", e.current, 0, 100)
		if err != nil {
			return config.Monitor{}, err
		}
		pts = []point{{Minute: 0, Brightness: b}}
	default:
		pts, err = askCustomPoints(p, e.current)
		if err != nil {
			return config.Monitor{}, err
		}
	}
	pts = dedupeAndSort(pts)
	p.Say("  schedule: %s\n", formatPoints(pts))
	return config.Monitor{Name: name, Match: e.info.Key(), Points: pts, Night: carry}, nil
}

func askCustomPoints(p *prompt.Prompter, current int) ([]point, error) {
	var pts []point
	seen := map[int]bool{}
	for i := 0; i < 100; i++ {
		clock, err := p.String("Key point time as HH:MM (empty to finish)", "", func(v string) error {
			if v == "" {
				return nil
			}
			_, err := config.ParseClock(v)
			return err
		})
		if err != nil {
			return nil, err
		}
		if clock == "" {
			if len(pts) == 0 {
				p.Say("  add at least one key point")
				continue
			}
			return pts, nil
		}
		minute, _ := config.ParseClock(clock)
		if seen[minute] {
			p.Say("  %s is already used", clock)
			continue
		}
		b, err := p.Int(fmt.Sprintf("Brightness at %s", clock), current, 0, 100)
		if err != nil {
			return nil, err
		}
		seen[minute] = true
		pts = append(pts, point{Minute: minute, Brightness: b})
	}
	return nil, errors.New("too many key points")
}

func dedupeAndSort(pts []point) []point {
	m := map[int]int{}
	for _, p := range pts {
		m[p.Minute] = p.Brightness
	}
	out := make([]point, 0, len(m))
	for minute, b := range m {
		out = append(out, point{Minute: minute, Brightness: b})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Minute < out[j-1].Minute; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func formatPoints(pts []point) string {
	parts := make([]string, len(pts))
	for i, p := range pts {
		parts[i] = fmt.Sprintf("%s -> %d%%", config.FormatClock(p.Minute), p.Brightness)
	}
	return strings.Join(parts, ", ")
}

func askNight(p *prompt.Prompter, cfg, existing *config.Config, entries []*entry, backend bool) (config.Night, error) {
	n := config.Night{
		Start: 21 * 60, End: 7 * 60,
		Ramp: config.DefaultRamp, BlueGain: config.DefaultBlueGain, RedGain: 100, GreenGain: 100,
		Fallback: config.DefaultFallback, Enabled: true,
	}
	if existing != nil && existing.Night.Enabled {
		n = existing.Night
	}
	wasEnabled := existing == nil || existing.Night.Enabled

	p.Say("\nNight mode warms the screen colors (less blue) in the evening and fades back in the morning.")
	enable, err := p.Confirm("Enable night mode?", wasEnabled)
	if err != nil {
		return n, err
	}
	if existing != nil {
		n.RedGain, n.GreenGain = existing.Night.RedGain, existing.Night.GreenGain
		n.Fallback = existing.Night.Fallback
	}
	if !enable {
		n.Enabled = false
		return n, nil
	}
	for try := 0; try < 5; try++ {
		start, err := p.Clock("Night mode starts at", config.FormatClock(n.Start))
		if err != nil {
			return n, err
		}
		end, err := p.Clock("Night mode ends at", config.FormatClock(n.End))
		if err != nil {
			return n, err
		}
		ramp, err := p.String("Fade duration, how long the change takes (for example 30m, 1h)", config.FormatDuration(n.Ramp), func(v string) error {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return fmt.Errorf("%q is not a positive duration such as 30m", v)
			}
			return nil
		})
		if err != nil {
			return n, err
		}
		blue, err := p.Int("Blue light at the peak, in percent of the original (lower is warmer)", n.BlueGain, 0, 100)
		if err != nil {
			return n, err
		}
		cand := n
		cand.Enabled = true
		cand.Start, _ = config.ParseClock(start)
		cand.End, _ = config.ParseClock(end)
		cand.Ramp, _ = time.ParseDuration(ramp)
		cand.BlueGain = blue

		trial := *cfg
		trial.Night = cand
		if _, err := config.Parse(config.Marshal(&trial, nil)); err != nil {
			p.Say("  that night window is not valid: %v\n  Let's try again.", err)
			n = cand
			continue
		}
		n = cand
		return askFallback(p, n, entries, backend)
	}
	return n, errors.New("could not get a valid night window")
}

// askColorLevels lets each monitor with color control have its own night color
// level instead of the global one. cfg.Monitors[i] belongs to entries[i].
func askColorLevels(p *prompt.Prompter, cfg *config.Config, entries []*entry) error {
	if !cfg.Night.Enabled {
		return nil
	}
	asked := false
	for i, e := range entries {
		if !e.rgb {
			continue
		}
		m := &cfg.Monitors[i]
		if !asked {
			p.Say("\nEach monitor can have its own night color level (panels differ). Press Enter to keep the global one (blue %d%%).", cfg.Night.BlueGain)
			asked = true
		}
		use, err := p.Confirm(fmt.Sprintf("Use a different night color level for %s?", m.Name), !m.Night.IsZero())
		if err != nil {
			return err
		}
		if !use {
			m.Night = config.MonitorNight{}
			continue
		}
		cur := m.NightFor(cfg.Night)
		blue, err := p.Int(fmt.Sprintf("Blue light at the peak for %s, in percent (global is %d)", m.Name, cfg.Night.BlueGain), cur.BlueGain, 0, 100)
		if err != nil {
			return err
		}
		night := config.MonitorNight{BlueGain: &blue}
		more, err := p.Confirm(fmt.Sprintf("Also adjust red and green for %s?", m.Name), m.Night.RedGain != nil || m.Night.GreenGain != nil)
		if err != nil {
			return err
		}
		if more {
			red, err := p.Int(fmt.Sprintf("Red at the peak for %s, in percent", m.Name), cur.RedGain, 0, 100)
			if err != nil {
				return err
			}
			green, err := p.Int(fmt.Sprintf("Green at the peak for %s, in percent", m.Name), cur.GreenGain, 0, 100)
			if err != nil {
				return err
			}
			night.RedGain, night.GreenGain = &red, &green
		}
		m.Night = night
	}
	return nil
}

func askFallback(p *prompt.Prompter, n config.Night, entries []*entry, backend bool) (config.Night, error) {
	var without []string
	for _, e := range entries {
		if e.bright && !e.rgb {
			without = append(without, e.info.Model)
		}
	}
	if len(without) == 0 {
		return n, nil
	}
	p.Say("\nThese monitors cannot change color through DDC: %s.", strings.Join(without, ", "))
	if !backend {
		p.Say("No desktop night light was detected, so they will not get night mode.")
		n.Fallback = config.FallbackAuto
		return n, nil
	}
	use, err := p.Confirm("Use the desktop night light as a fallback for them?", n.Fallback != config.FallbackOff)
	if err != nil {
		return n, err
	}
	n.Fallback = config.FallbackOff
	if use {
		n.Fallback = config.FallbackAuto
	}
	return n, nil
}

// slug turns a model name into a short lowercase identifier.
func slug(model string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(model) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "monitor"
	}
	return s
}

// uniqueName returns base, or base-2, base-3... if base is taken by someone else.
func uniqueName(base string, used map[string]bool, own string) string {
	if base == own || !used[base] {
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !used[cand] {
			return cand
		}
	}
}

// writeFile writes data atomically. An existing file is first copied to
// <path>.bak-<timestamp>; the backup path is returned ("" if there was none).
func writeFile(path string, data []byte, now time.Time) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	backup := ""
	if old, err := os.ReadFile(path); err == nil {
		backup = path + ".bak-" + now.Format("20060102-150405")
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return "", fmt.Errorf("saving the backup: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return backup, nil
}
