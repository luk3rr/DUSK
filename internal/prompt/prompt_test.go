package prompt

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func newP(in string, yes bool) (*Prompter, *bytes.Buffer) {
	var out bytes.Buffer
	return New(strings.NewReader(in), &out, yes), &out
}

func TestStringDefaultAndValue(t *testing.T) {
	p, out := newP("\nhello\n", false)
	if v, err := p.String("Name", "dflt", nil); err != nil || v != "dflt" {
		t.Errorf("empty answer = %q, %v", v, err)
	}
	if v, err := p.String("Name", "dflt", nil); err != nil || v != "hello" {
		t.Errorf("typed answer = %q, %v", v, err)
	}
	if !strings.Contains(out.String(), "Name [dflt]: ") {
		t.Errorf("prompt text: %q", out.String())
	}
}

func TestStringRetriesOnInvalidInput(t *testing.T) {
	p, out := newP("bad\nstill bad\ngood\n", false)
	v, err := p.String("X", "", func(s string) error {
		if s != "good" {
			return errors.New("must be good")
		}
		return nil
	})
	if err != nil || v != "good" {
		t.Fatalf("%q %v", v, err)
	}
	if strings.Count(out.String(), "must be good") != 2 {
		t.Errorf("the reason should be printed twice:\n%s", out.String())
	}
}

func TestStringGivesUpAfterTooManyTries(t *testing.T) {
	p, _ := newP(strings.Repeat("no\n", 20), false)
	if _, err := p.String("X", "", func(string) error { return errors.New("never") }); err == nil || errors.Is(err, ErrAborted) {
		t.Errorf("expected a too-many-answers error, got %v", err)
	}
}

func TestEOFAborts(t *testing.T) {
	p, _ := newP("", false)
	if _, err := p.String("X", "d", nil); !errors.Is(err, ErrAborted) {
		t.Errorf("err = %v", err)
	}
	// A last answer without a trailing newline still counts.
	p, _ = newP("last", false)
	if v, err := p.String("X", "d", nil); err != nil || v != "last" {
		t.Errorf("%q %v", v, err)
	}
}

func TestInt(t *testing.T) {
	p, out := newP("abc\n150\n42\n\n", false)
	if v, err := p.Int("Level", 10, 0, 100); err != nil || v != 42 {
		t.Errorf("%d %v", v, err)
	}
	if !strings.Contains(out.String(), "is not a number") || !strings.Contains(out.String(), "outside 0-100") {
		t.Errorf("messages missing:\n%s", out.String())
	}
	if v, err := p.Int("Level", 10, 0, 100); err != nil || v != 10 {
		t.Errorf("default = %d %v", v, err)
	}
}

func TestClock(t *testing.T) {
	p, _ := newP("25:00\n7:30\n07:30\n", false)
	if v, err := p.Clock("When", "08:00"); err != nil || v != "07:30" {
		t.Errorf("%q %v", v, err)
	}
}

func TestConfirm(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "YES\n": true, "n\n": false, "No\n": false, "\n": true} {
		p, _ := newP(in, false)
		if got, err := p.Confirm("Sure?", true); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	p, _ := newP("\n", false)
	if got, _ := p.Confirm("Sure?", false); got {
		t.Error("default false should stay false")
	}
	p, out := newP("maybe\ny\n", false)
	if got, err := p.Confirm("Sure?", false); err != nil || !got || !strings.Contains(out.String(), "answer y or n") {
		t.Errorf("%v %v %q", got, err, out.String())
	}
}

func TestChoice(t *testing.T) {
	opts := []string{"one", "two", "three"}
	p, out := newP("9\n2\n\n", false)
	if i, err := p.Choice("Pick:", opts, 0); err != nil || i != 1 {
		t.Errorf("%d %v", i, err)
	}
	if !strings.Contains(out.String(), "1) one") || !strings.Contains(out.String(), "enter a number from 1 to 3") {
		t.Errorf("output:\n%s", out.String())
	}
	if i, err := p.Choice("Pick:", opts, 2); err != nil || i != 2 {
		t.Errorf("default = %d %v", i, err)
	}
}

func TestYesModeNeverReads(t *testing.T) {
	p, out := newP("", true) // no input at all
	if v, err := p.String("Name", "dflt", nil); err != nil || v != "dflt" {
		t.Errorf("%q %v", v, err)
	}
	if v, err := p.Int("N", 7, 0, 10); err != nil || v != 7 {
		t.Errorf("%d %v", v, err)
	}
	if v, err := p.Confirm("Ok?", true); err != nil || !v {
		t.Errorf("%v %v", v, err)
	}
	if v, err := p.Choice("Pick", []string{"a", "b"}, 1); err != nil || v != 1 {
		t.Errorf("%d %v", v, err)
	}
	if !strings.Contains(out.String(), "Name: dflt") {
		t.Errorf("defaults should be echoed:\n%s", out.String())
	}
	if _, err := p.String("Bad", "", func(string) error { return errors.New("no") }); err == nil {
		t.Error("an invalid default must be an error in yes mode")
	}
}
