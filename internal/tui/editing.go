// Filename: internal/tui/editing.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"dusk/internal/config"
	"dusk/internal/gamma"
)

// ---- field checks: each returns the message shown under the field ----

func vClock(v string) error {
	if _, err := config.ParseClock(strings.TrimSpace(v)); err != nil {
		return errors.New("use HH:MM, for example 21:30")
	}
	return nil
}

func vPercent(v string) error {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > 100 {
		return errors.New("a whole number from 0 to 100")
	}
	return nil
}

func vOptPercent(v string) error {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return vPercent(v)
}

func vFloor(v string) error {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > config.MaxFloor {
		return fmt.Errorf("a whole number from 0 to %d", config.MaxFloor)
	}
	return nil
}

func vDuration(v string) error {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil || d <= 0 {
		return errors.New("a duration such as 30m or 1h")
	}
	return nil
}

func vUnique(what string, others map[string]bool) func(string) error {
	return func(v string) error {
		switch {
		case strings.TrimSpace(v) == "":
			return fmt.Errorf("the %s cannot be empty", what)
		case v != strings.TrimSpace(v):
			return fmt.Errorf("the %s cannot start or end with spaces", what)
		case others[strings.ToLower(v)]:
			return fmt.Errorf("another monitor already uses this %s", what)
		}
		return nil
	}
}

func atoi(v string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

func optPtr(v string) *int {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	n := atoi(v, 0)
	return &n
}

func ptrText(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

// problems turns a validation error into one line for the form.
func problems(err error) string {
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		return strings.Join(ve.Problems, "; ")
	}
	return err.Error()
}

// ---- monitor editor ----

func pointFields(i int, p config.Point) []field {
	return []field{
		{label: fmt.Sprintf("Point %d time", i+1), group: i, hint: "HH:MM", value: config.FormatClock(p.Minute), validate: vClock},
		{label: fmt.Sprintf("Point %d level %%", i+1), group: i, hint: "0-100", value: strconv.Itoa(p.Brightness), validate: vPercent},
	}
}

// monitorForm builds the editor for mon. index is the monitor's position in cfg, or -1 for a new one.
func monitorForm(cfg *config.Config, mon config.Monitor, index int) form {
	names, matches := map[string]bool{}, map[string]bool{}
	for i, m := range cfg.Monitors {
		if i != index {
			names[strings.ToLower(m.Name)] = true
			matches[strings.ToLower(m.Match)] = true
		}
	}
	title := "Edit monitor"
	if index < 0 {
		title = "New monitor"
	}
	f := form{title: title, dirty: index < 0}
	f.fields = []field{
		{label: "Name", group: -1, value: mon.Name, validate: vUnique("name", names)},
		{label: "Match", group: -1, value: mon.Match, hint: `"model" or "model#serial"`, validate: vUnique("match", matches)},
		{label: "Brightness", group: -1, kind: kindChoice, value: orDefault(mon.Brightness, config.BrightnessSoftware), choices: []string{config.BrightnessSoftware, config.BrightnessDDC}},
		{label: "Floor %", group: -1, value: strconv.Itoa(mon.Floor), hint: "0-50", validate: vFloor},
		{label: "Night red %", group: -1, value: ptrText(mon.Night.RedGain), hint: "empty = global", validate: vOptPercent},
		{label: "Night green %", group: -1, value: ptrText(mon.Night.GreenGain), hint: "empty = global", validate: vOptPercent},
		{label: "Night blue %", group: -1, value: ptrText(mon.Night.BlueGain), hint: "empty = global", validate: vOptPercent},
	}
	for i, p := range mon.Points {
		f.fields = append(f.fields, pointFields(i, p)...)
	}
	return f
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// addPoint appends a schedule point after the last one and focuses it.
func (f *form) addPoint() {
	n := 0
	for _, fl := range f.fields {
		if fl.group >= n {
			n = fl.group + 1
		}
	}
	f.fields = append(f.fields, pointFields(n, config.Point{Minute: 12 * 60, Brightness: 50})...)
	f.focus = len(f.fields) - 2
	f.dirty = true
}

// deletePoint removes the schedule point under the focus and renumbers the rest.
func (f *form) deletePoint() {
	g := f.fields[f.focus].group
	if g < 0 {
		return
	}
	var kept []field
	for _, fl := range f.fields {
		if fl.group != g {
			kept = append(kept, fl)
		}
	}
	// Renumber labels and groups.
	next := -1
	last := -2
	for i := range kept {
		if kept[i].group < 0 {
			continue
		}
		if kept[i].group != last {
			last = kept[i].group
			next++
		}
		kept[i].group = next
		if strings.HasSuffix(kept[i].label, "time") {
			kept[i].label = fmt.Sprintf("Point %d time", next+1)
		} else {
			kept[i].label = fmt.Sprintf("Point %d level %%", next+1)
		}
	}
	f.fields = kept
	f.focus = min(f.focus, len(f.fields)-1)
	f.dirty = true
}

// monitorFromForm reads a validated form back into a config.Monitor.
func monitorFromForm(f *form) config.Monitor {
	mon := config.Monitor{
		Name:       f.get("Name"),
		Match:      f.get("Match"),
		Brightness: f.get("Brightness"),
		Floor:      atoi(f.get("Floor %"), config.DefaultFloor),
		Night: config.MonitorNight{
			RedGain:   optPtr(f.get("Night red %")),
			GreenGain: optPtr(f.get("Night green %")),
			BlueGain:  optPtr(f.get("Night blue %")),
		},
	}
	byGroup := map[int]*config.Point{}
	var order []int
	for _, fl := range f.fields {
		if fl.group < 0 {
			continue
		}
		p := byGroup[fl.group]
		if p == nil {
			p = &config.Point{}
			byGroup[fl.group] = p
			order = append(order, fl.group)
		}
		if strings.HasSuffix(fl.label, "time") {
			p.Minute, _ = config.ParseClock(strings.TrimSpace(fl.value))
		} else {
			p.Brightness = atoi(fl.value, 0)
		}
	}
	for _, g := range order {
		mon.Points = append(mon.Points, *byGroup[g])
	}
	sort.Slice(mon.Points, func(a, b int) bool { return mon.Points[a].Minute < mon.Points[b].Minute })
	return mon
}

// duplicateTimes marks schedule rows whose time is used twice.
func (f *form) duplicateTimes() bool {
	seen := map[int]bool{}
	bad := false
	for i := range f.fields {
		fl := &f.fields[i]
		if fl.group < 0 || !strings.HasSuffix(fl.label, "time") {
			continue
		}
		m, err := config.ParseClock(strings.TrimSpace(fl.value))
		if err != nil {
			continue
		}
		if seen[m] {
			fl.err = "this time is already in the schedule"
			if !bad {
				f.focus = i
			}
			bad = true
		}
		seen[m] = true
	}
	return bad
}

// ---- night editor ----

func nightForm(n config.Night) form {
	on := "off"
	if n.Enabled {
		on = "on"
	}
	f := form{title: "Night mode"}
	f.fields = []field{
		{label: "Night mode", group: -1, kind: kindChoice, value: on, choices: []string{"on", "off"}},
		{label: "Start", group: -1, value: config.FormatClock(n.Start), hint: "HH:MM"},
		{label: "End", group: -1, value: config.FormatClock(n.End), hint: "HH:MM"},
		{label: "Fade ramp", group: -1, value: config.FormatDuration(orRamp(n.Ramp)), hint: "30m"},
		{label: "Red %", group: -1, value: strconv.Itoa(n.RedGain), hint: "0-100", validate: vPercent},
		{label: "Green %", group: -1, value: strconv.Itoa(n.GreenGain), hint: "0-100", validate: vPercent},
		{label: "Blue %", group: -1, value: strconv.Itoa(n.BlueGain), hint: "0-100", validate: vPercent},
	}
	setNightValidators(&f)
	return f
}

func orRamp(d time.Duration) time.Duration {
	if d <= 0 {
		return config.DefaultRamp
	}
	return d
}

// setNightValidators makes start, end and ramp required only while night mode is on.
func setNightValidators(f *form) {
	req := func(check func(string) error) func(string) error {
		return func(v string) error {
			if f.get("Night mode") == "off" {
				return nil
			}
			return check(v)
		}
	}
	for i := range f.fields {
		switch f.fields[i].label {
		case "Start", "End":
			f.fields[i].validate = req(vClock)
		case "Fade ramp":
			f.fields[i].validate = req(vDuration)
		}
	}
}

func nightFromForm(f *form, base config.Night) config.Night {
	n := base
	n.Enabled = f.get("Night mode") == "on"
	if n.Enabled {
		n.Start, _ = config.ParseClock(strings.TrimSpace(f.get("Start")))
		n.End, _ = config.ParseClock(strings.TrimSpace(f.get("End")))
		n.Ramp, _ = time.ParseDuration(strings.TrimSpace(f.get("Fade ramp")))
	}
	n.RedGain = atoi(f.get("Red %"), 100)
	n.GreenGain = atoi(f.get("Green %"), 100)
	n.BlueGain = atoi(f.get("Blue %"), config.DefaultBlueGain)
	return n
}

// ---- defaults for a first run ----

// defaultConfig builds a configuration with one entry per detected screen.
func defaultConfig(screens []gamma.Output) *config.Config {
	cfg := &config.Config{
		PollInterval:   config.DefaultPollInterval,
		RescanInterval: config.DefaultRescanInterval,
		VerifyInterval: config.DefaultVerifyInterval,
		Night: config.Night{
			Enabled: true, Start: 21 * 60, End: 7 * 60, Ramp: config.DefaultRamp,
			BlueGain: config.DefaultBlueGain, RedGain: 100, GreenGain: 100,
		},
	}
	used := map[string]bool{}
	for _, s := range screens {
		name := orDefault(s.Product, s.Name)
		for i := 2; used[strings.ToLower(name)]; i++ {
			name = fmt.Sprintf("%s %d", orDefault(s.Product, s.Name), i)
		}
		used[strings.ToLower(name)] = true
		cfg.Monitors = append(cfg.Monitors, newMonitor(name, s.Key()))
	}
	return cfg
}

func newMonitor(name, match string) config.Monitor {
	return config.Monitor{
		Name: name, Match: match, Brightness: config.BrightnessSoftware, Floor: config.DefaultFloor,
		Points: []config.Point{{Minute: 8 * 60, Brightness: 70}, {Minute: 18 * 60, Brightness: 50}, {Minute: 22 * 60, Brightness: 30}},
	}
}
