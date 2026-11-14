// Package prompt asks questions on a terminal (or any reader and writer).
// Every question shows its default in brackets and Enter accepts it. In "yes"
// mode nothing is asked and every call returns its default.
package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"dusk/internal/config"
)

// ErrAborted means the input ended before the questions were answered.
var ErrAborted = errors.New("input ended before the setup was finished")

const maxTries = 5

// Prompter asks questions.
type Prompter struct {
	in  *bufio.Reader
	out io.Writer
	yes bool
}

// New creates a Prompter. With yes set, defaults are taken without asking.
func New(in io.Reader, out io.Writer, yes bool) *Prompter {
	return &Prompter{in: bufio.NewReader(in), out: out, yes: yes}
}

// Say prints a line of text.
func (p *Prompter) Say(format string, args ...any) {
	fmt.Fprintf(p.out, format+"\n", args...)
}

func (p *Prompter) readLine() (string, error) {
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		if errors.Is(err, io.EOF) {
			return "", ErrAborted
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// String asks for text. An empty answer returns def. validate may be nil; it
// is applied to the final value (including an empty one).
func (p *Prompter) String(label, def string, validate func(string) error) (string, error) {
	check := func(v string) error {
		if validate == nil {
			return nil
		}
		return validate(v)
	}
	if p.yes {
		fmt.Fprintf(p.out, "%s: %s\n", label, def)
		if err := check(def); err != nil {
			return "", fmt.Errorf("default for %q is not valid: %w", label, err)
		}
		return def, nil
	}
	for try := 0; try < maxTries; try++ {
		if def != "" {
			fmt.Fprintf(p.out, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(p.out, "%s: ", label)
		}
		line, err := p.readLine()
		if err != nil {
			fmt.Fprintln(p.out)
			return "", err
		}
		if line == "" {
			line = def
		}
		if err := check(line); err != nil {
			fmt.Fprintf(p.out, "  %v\n", err)
			continue
		}
		return line, nil
	}
	return "", fmt.Errorf("too many invalid answers for %q", label)
}

// Clock asks for a time of day as HH:MM.
func (p *Prompter) Clock(label, def string) (string, error) {
	return p.String(label, def, func(v string) error {
		_, err := config.ParseClock(v)
		return err
	})
}

// Int asks for an integer in [lo, hi].
func (p *Prompter) Int(label string, def, lo, hi int) (int, error) {
	v, err := p.String(fmt.Sprintf("%s (%d-%d)", label, lo, hi), strconv.Itoa(def), func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%q is not a number", v)
		}
		if n < lo || n > hi {
			return fmt.Errorf("%d is outside %d-%d", n, lo, hi)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	n, _ := strconv.Atoi(v)
	return n, nil
}

// Confirm asks a yes/no question.
func (p *Prompter) Confirm(label string, def bool) (bool, error) {
	hint, defText := "y/N", "n"
	if def {
		hint, defText = "Y/n", "y"
	}
	v, err := p.String(fmt.Sprintf("%s (%s)", label, hint), defText, func(v string) error {
		switch strings.ToLower(v) {
		case "y", "yes", "n", "no":
			return nil
		}
		return fmt.Errorf("answer y or n")
	})
	if err != nil {
		return false, err
	}
	return strings.HasPrefix(strings.ToLower(v), "y"), nil
}

// Choice shows numbered options and returns the index of the chosen one.
// def is the index used when the answer is empty.
func (p *Prompter) Choice(label string, options []string, def int) (int, error) {
	fmt.Fprintf(p.out, "%s\n", label)
	for i, o := range options {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, o)
	}
	v, err := p.String("Choose", strconv.Itoa(def+1), func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > len(options) {
			return fmt.Errorf("enter a number from 1 to %d", len(options))
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	n, _ := strconv.Atoi(v)
	return n - 1, nil
}
