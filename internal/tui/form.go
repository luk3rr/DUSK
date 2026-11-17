// Filename: internal/tui/form.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type fieldKind int

const (
	kindText   fieldKind = iota // free text, typed by the user
	kindChoice                  // one of a fixed list, changed with left/right or space
)

type field struct {
	label    string
	kind     fieldKind
	value    string
	choices  []string
	hint     string
	group    int // rows that belong together (a schedule point); -1 for plain fields
	validate func(string) error
	err      string
}

// form is a vertical list of fields with one focused field.
type form struct {
	title   string
	fields  []field
	focus   int
	dirty   bool
	errLine string // an error that belongs to no single field
	scroll  int
}

type formAction int

const (
	actNone formAction = iota
	actSave
	actCancel
	actAddRow
	actDeleteRow
)

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
		if cur.kind == kindChoice {
			f.cycle(-1)
		}
	case "right", " ":
		if cur.kind == kindChoice {
			f.cycle(1)
		} else if k.String() == " " {
			f.typeRune(' ')
		}
	case "backspace":
		if cur.kind == kindText && cur.value != "" {
			r := []rune(cur.value)
			cur.value = string(r[:len(r)-1])
			f.touched()
		}
	default:
		if k.Type == tea.KeyRunes && cur.kind == kindText {
			for _, r := range k.Runes {
				f.typeRune(r)
			}
		}
	}
	return actNone
}

func (f *form) typeRune(r rune) {
	cur := &f.fields[f.focus]
	if cur.kind != kindText || r < ' ' || r > '~' {
		return
	}
	cur.value += string(r)
	f.touched()
}

func (f *form) touched() {
	f.dirty = true
	f.fields[f.focus].err = ""
	f.errLine = ""
}

func (f *form) move(d int) {
	f.focus = (f.focus + d + len(f.fields)) % len(f.fields)
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

// validate runs every field's check, records the messages and moves the focus
// to the first bad field. It reports whether everything is fine.
func (f *form) validate() bool {
	first := -1
	for i := range f.fields {
		fl := &f.fields[i]
		fl.err = ""
		if fl.validate == nil {
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

func (f *form) get(label string) string {
	for _, fl := range f.fields {
		if fl.label == label {
			return fl.value
		}
	}
	return ""
}

// render draws the form in at most height lines of the given width.
func (f *form) render(width, height int, active bool) string {
	var lines []string
	focusLine := 0
	for i, fl := range f.fields {
		marker := "  "
		if active && i == f.focus {
			marker = "> "
			focusLine = len(lines)
		}
		val := fl.value
		switch {
		case fl.kind == kindChoice:
			val = "< " + val + " >"
		case active && i == f.focus:
			val += "_"
		}
		line := fmt.Sprintf("%s%-18s %s", marker, fl.label, val)
		if fl.hint != "" && fl.value == "" {
			line += dimStyle.Render("  " + fl.hint)
		}
		lines = append(lines, line)
		if fl.err != "" {
			lines = append(lines, errStyle.Render("    ! "+fl.err))
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
