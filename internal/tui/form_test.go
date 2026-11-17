// Filename: internal/tui/form_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBar(t *testing.T) {
	cases := []struct {
		v, lo, hi, w int
		want         string
	}{
		{0, 0, 100, 11, "[o----------]"},
		{100, 0, 100, 11, "[==========o]"},
		{50, 0, 100, 11, "[=====o-----]"},
		{-5, 0, 100, 11, "[o----------]"},
		{500, 0, 100, 11, "[==========o]"},
		{3400, 1700, 6500, 11, "[===o-------]"},
	}
	for _, c := range cases {
		if got := bar(c.v, c.lo, c.hi, c.w); got != c.want {
			t.Errorf("bar(%d,%d,%d,%d) = %q, want %q", c.v, c.lo, c.hi, c.w, got, c.want)
		}
	}
}

func sliderForm() *form {
	return &form{fields: []field{
		{label: "Level", group: -1, kind: kindSlider, value: "50", min: 0, max: 100, step: 1, big: 10, unit: "%"},
		{label: "Own", group: -1, kind: kindSlider, value: "", min: 0, max: 100, step: 1, big: 10, optional: true, def: 40, inheritText: "global"},
		{label: "At", group: -1, kind: kindTime, value: "23:50"},
	}}
}

func press(f *form, typ tea.KeyType, n int) {
	for i := 0; i < n; i++ {
		f.key(tea.KeyMsg{Type: typ})
	}
}

func TestSliderKeys(t *testing.T) {
	f := sliderForm()
	press(f, tea.KeyRight, 3)
	if f.fields[0].value != "53" {
		t.Errorf("3 x Right = %s", f.fields[0].value)
	}
	press(f, tea.KeyShiftLeft, 2)
	if f.fields[0].value != "33" {
		t.Errorf("2 x Shift+Left = %s", f.fields[0].value)
	}
	press(f, tea.KeyPgUp, 1)
	if f.fields[0].value != "43" {
		t.Errorf("PgUp = %s", f.fields[0].value)
	}
	press(f, tea.KeyEnd, 1)
	if f.fields[0].value != "100" {
		t.Errorf("End = %s", f.fields[0].value)
	}
	press(f, tea.KeyRight, 5) // clamped at the maximum
	if f.fields[0].value != "100" {
		t.Errorf("past the maximum = %s", f.fields[0].value)
	}
	press(f, tea.KeyHome, 1)
	press(f, tea.KeyLeft, 5)
	if f.fields[0].value != "0" {
		t.Errorf("below the minimum = %s", f.fields[0].value)
	}
	if !f.dirty {
		t.Error("moving a slider marks the form as changed")
	}
}

func TestTypingDoesNotChangeASlider(t *testing.T) {
	f := sliderForm()
	f.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("99x")})
	if f.fields[0].value != "50" {
		t.Errorf("typed text reached a slider: %q", f.fields[0].value)
	}
}

func TestOptionalSliderInheritsUntilMoved(t *testing.T) {
	f := sliderForm()
	f.focus = 1
	if v := f.fields[1].display(false); !strings.Contains(v, "global") {
		t.Errorf("an unset slider says what it inherits: %q", v)
	}
	press(f, tea.KeyRight, 1)
	if f.fields[1].value != "40" {
		t.Errorf("the first move starts at the default: %q", f.fields[1].value)
	}
	press(f, tea.KeyRight, 2)
	if f.fields[1].value != "42" {
		t.Errorf("then it moves by the step: %q", f.fields[1].value)
	}
	press(f, tea.KeyBackspace, 1)
	if f.fields[1].value != "" {
		t.Errorf("Backspace goes back to inheriting: %q", f.fields[1].value)
	}
}

func TestTimeFieldSteps(t *testing.T) {
	f := sliderForm()
	f.focus = 2
	press(f, tea.KeyRight, 1)
	if f.fields[2].value != "00:05" {
		t.Errorf("23:50 + 15 min = %s", f.fields[2].value)
	}
	press(f, tea.KeyLeft, 1)
	press(f, tea.KeyShiftLeft, 1)
	if f.fields[2].value != "22:50" {
		t.Errorf("-15 then -60 = %s", f.fields[2].value)
	}
	f.fields[2].value = "oops"
	press(f, tea.KeyRight, 1)
	if f.fields[2].value != "oops" {
		t.Error("an unreadable time must be left alone")
	}
	f.fields[2].value = ""
	f.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("07:30")})
	if f.fields[2].value != "07:30" {
		t.Errorf("a time can still be typed: %q", f.fields[2].value)
	}
}

func TestHiddenFieldsAreSkippedAndNotValidated(t *testing.T) {
	hide := true
	f := &form{fields: []field{
		{label: "A", group: -1},
		{label: "B", group: -1, hide: func() bool { return hide }, validate: func(string) error { return errTest }},
		{label: "C", group: -1},
	}}
	press(f, tea.KeyDown, 1)
	if f.focus != 2 {
		t.Errorf("Down should skip the hidden field, focus = %d", f.focus)
	}
	press(f, tea.KeyUp, 1)
	if f.focus != 0 {
		t.Errorf("Up should skip it too, focus = %d", f.focus)
	}
	if !f.validate() {
		t.Error("a hidden field must not be validated")
	}
	hide = false
	if f.validate() || f.focus != 1 {
		t.Errorf("a visible bad field fails and takes the focus (focus %d)", f.focus)
	}
	if strings.Contains(f.render(80, 20, true), "never") {
		t.Error("unexpected text")
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

const errTest = testErr("bad value")

func TestRenderShowsBarsAndValues(t *testing.T) {
	f := sliderForm()
	f.focus = 2
	out := f.render(80, 20, true)
	for _, want := range []string{"[============o-------------]  50 %", "global", "23:50_"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
}

func TestSwatchHasTheRightWidth(t *testing.T) {
	if got := stripANSI(swatch(100, 72, 47)); len(got) != 8 {
		t.Errorf("swatch = %q", got)
	}
}
