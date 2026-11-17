// Filename: internal/tui/form.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"dusk/internal/config"
)

type fieldKind int

const (
	kindText   fieldKind = iota // free text, typed by the user
	kindChoice                  // one of a fixed list, changed with left/right or space
	kindSlider                  // a whole number in a range, moved with the arrow keys
	kindTime                    // HH:MM, typed or moved in steps with the arrow keys
)

const sliderWidth = 26

type field struct {
	label    string
	kind     fieldKind
	value    string
	choices  []string
	hint     string
	group    int // rows that belong together (a schedule point); -1 for plain fields
	validate func(string) error
	err      string

	// kindSlider
	min, max, step, big int
	unit                string
	optional            bool // an empty value means "inherit" and shows as inheritText
	def                 int  // where an inherited slider starts when first moved
	inheritText         string

	hide     func() bool   // the field is not shown (and skipped by the focus) while this is true
	extra    func() string // lines drawn under the field
	onChange func()        // runs after the user changes the value
}

// form is a vertical list of fields with one focused field.
type form struct {
	title   string
	fields  []field
	focus   int
	dirty   bool
	errLine string // an error that belongs to no single field
	scroll  int
	// lastMode is the color mode chosen before the current one (see switchColorMode).
	lastMode string
}

type formAction int

const (
	actNone formAction = iota
	actSave
	actCancel
	actAddRow
	actDeleteRow
)

func (f *form) hidden(i int) bool { return f.fields[i].hide != nil && f.fields[i].hide() }

// key handles one key press and tells the owner when the user wants to save or leave.
func (f *form) key(k tea.KeyMsg) formAction {
	if len(f.fields) == 0 {
		return actNone
	}
	cur := &f.fields[f.focus]
	switch k.String() {
	case "ctrl+s":
		return actSave
	case "esc":
		return actCancel
	case "ctrl+a":
		return actAddRow
	case "ctrl+d":
		return actDeleteRow
	case "tab", "down":
		f.move(1)
	case "shift+tab", "up":
		f.move(-1)
	case "left":
		f.adjust(cur, -1, false)
	case "right":
		f.adjust(cur, 1, false)
	case "shift+left", "pgdown":
		f.adjust(cur, -1, true)
	case "shift+right", "pgup":
		f.adjust(cur, 1, true)
	case "home":
		if cur.kind == kindSlider {
			f.setSlider(cur, cur.min)
		}
	case "end":
		if cur.kind == kindSlider {
			f.setSlider(cur, cur.max)
		}
	case " ":
		if cur.kind == kindChoice {
			f.cycle(1)
		} else {
			f.typeRune(' ')
		}
	case "backspace", "delete":
		switch {
		case cur.kind == kindSlider && cur.optional && cur.value != "":
			cur.value = ""
			f.touched()
		case (cur.kind == kindText || cur.kind == kindTime) && cur.value != "":
			r := []rune(cur.value)
			cur.value = string(r[:len(r)-1])
			f.touched()
		}
	default:
		if k.Type == tea.KeyRunes {
			for _, r := range k.Runes {
				f.typeRune(r)
			}
		}
	}
	return actNone
}

// adjust handles left/right on the focused field; big selects the large step.
func (f *form) adjust(cur *field, dir int, big bool) {
	switch cur.kind {
	case kindChoice:
		f.cycle(dir)
	case kindSlider:
		step := cur.step
		if big {
			step = cur.big
		}
		v, err := strconv.Atoi(cur.value)
		if err != nil {
			v = cur.def // first move of an inherited slider starts at its default
			f.setSlider(cur, v)
			return
		}
		f.setSlider(cur, v+dir*step)
	case kindTime:
		m, err := config.ParseClock(strings.TrimSpace(cur.value))
		if err != nil {
			return
		}
		step := 15
		if big {
			step = 60
		}
		cur.value = config.FormatClock(((m+dir*step)%1440 + 1440) % 1440)
		f.touched()
	}
}

func (f *form) setSlider(cur *field, v int) {
	cur.value = strconv.Itoa(max(cur.min, min(cur.max, v)))
	f.touched()
}

func (f *form) typeRune(r rune) {
	cur := &f.fields[f.focus]
	if (cur.kind != kindText && cur.kind != kindTime) || r < ' ' || r > '~' {
		return
	}
	cur.value += string(r)
	f.touched()
}

func (f *form) touched() {
	f.dirty = true
	f.fields[f.focus].err = ""
	f.errLine = ""
	if oc := f.fields[f.focus].onChange; oc != nil {
		oc()
	}
}

func (f *form) move(d int) {
	for range f.fields {
		f.focus = (f.focus + d + len(f.fields)) % len(f.fields)
		if !f.hidden(f.focus) {
			return
		}
	}
}

func (f *form) cycle(d int) {
	cur := &f.fields[f.focus]
	if len(cur.choices) == 0 {
		return
	}
	i := 0
	for j, c := range cur.choices {
		if c == cur.value {
			i = j
		}
	}
	cur.value = cur.choices[(i+d+len(cur.choices))%len(cur.choices)]
	f.touched()
}

// validate runs every visible field's check, records the messages and moves
// the focus to the first bad field. It reports whether everything is fine.
func (f *form) validate() bool {
	first := -1
	for i := range f.fields {
		fl := &f.fields[i]
		fl.err = ""
		if fl.validate == nil || f.hidden(i) {
			continue
		}
		if err := fl.validate(fl.value); err != nil {
			fl.err = err.Error()
			if first < 0 {
				first = i
			}
		}
	}
	if first >= 0 {
		f.focus = first
		return false
	}
	return true
}

// get returns the value of the field with the given label.
func (f *form) get(label string) string {
	for _, fl := range f.fields {
		if fl.label == label {
			return fl.value
		}
	}
	return ""
}

// set changes the value of the field with the given label.
func (f *form) set(label, value string) {
	for i := range f.fields {
		if f.fields[i].label == label {
			f.fields[i].value = value
		}
	}
}

// bar draws a progress bar for v in [lo, hi]: "[=====o------]".
func bar(v, lo, hi, width int) string {
	if hi <= lo {
		hi = lo + 1
	}
	pos := (max(lo, min(hi, v)) - lo) * (width - 1) / (hi - lo)
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < width; i++ {
		switch {
		case i < pos:
			b.WriteByte('=')
		case i == pos:
			b.WriteByte('o')
		default:
			b.WriteByte('-')
		}
	}
	b.WriteByte(']')
	return b.String()
}

// swatch draws a block of the color a white screen takes with these gains (percent).
func swatch(r, g, b int) string {
	c := fmt.Sprintf("#%02x%02x%02x", r*255/100, g*255/100, b*255/100)
	return lipgloss.NewStyle().Background(lipgloss.Color(c)).Render("        ")
}

func (fl field) display(active bool) string {
	switch fl.kind {
	case kindChoice:
		return "< " + fl.value + " >"
	case kindSlider:
		v, err := strconv.Atoi(fl.value)
		if err != nil {
			return dimStyle.Render(bar(fl.min, fl.min, fl.max, sliderWidth)) + "  " + orDefault(fl.inheritText, "-")
		}
		unit := ""
		if fl.unit != "" {
			unit = " " + fl.unit
		}
		return bar(v, fl.min, fl.max, sliderWidth) + fmt.Sprintf("  %d%s", v, unit)
	}
	if active {
		return fl.value + "_"
	}
	return fl.value
}

// render draws the form in at most height lines of the given width.
func (f *form) render(width, height int, active bool) string {
	var lines []string
	focusLine := 0
	for i, fl := range f.fields {
		if f.hidden(i) {
			continue
		}
		marker := "  "
		if active && i == f.focus {
			marker = "> "
			focusLine = len(lines)
		}
		line := fmt.Sprintf("%s%-16s %s", marker, fl.label, fl.display(active && i == f.focus))
		if fl.hint != "" && fl.value == "" && fl.kind != kindSlider {
			line += dimStyle.Render("  " + fl.hint)
		}
		lines = append(lines, line)
		if fl.err != "" {
			lines = append(lines, errStyle.Render("    ! "+fl.err))
		}
		if fl.extra != nil {
			for _, l := range strings.Split(fl.extra(), "\n") {
				if l != "" {
					lines = append(lines, "  "+strings.Repeat(" ", 17)+l)
				}
			}
		}
	}
	if f.errLine != "" {
		lines = append(lines, errStyle.Render("! "+f.errLine))
	}
	if height < 3 {
		height = 3
	}
	if len(lines) > height {
		if focusLine < f.scroll {
			f.scroll = focusLine
		}
		if focusLine >= f.scroll+height-1 {
			f.scroll = focusLine - height + 2
		}
		f.scroll = max(0, min(f.scroll, len(lines)-height))
		lines = lines[f.scroll : f.scroll+height]
	} else {
		f.scroll = 0
	}
	return strings.Join(lines, "\n")
}
