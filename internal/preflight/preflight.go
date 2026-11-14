// Package preflight checks that this computer can run dusk and, when it
// cannot, says exactly what to fix.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"dusk/internal/ddc"
	"dusk/internal/nightlight"
)

// Status is the outcome of one check.
type Status int

// Check outcomes.
const (
	OK Status = iota
	Warn
	Fail
)

// Result is one line of the report.
type Result struct {
	Name   string
	Status Status
	Detail string
	Hint   string
}

// Report is the outcome of Check.
type Report struct {
	Results []Result
	// NightLight is true when a night-light backend was detected.
	NightLight bool
}

// Failed reports whether any check failed.
func (r Report) Failed() bool {
	for _, x := range r.Results {
		if x.Status == Fail {
			return true
		}
	}
	return false
}

// Print writes the report in a human-readable form.
func (r Report) Print(w io.Writer) {
	for _, x := range r.Results {
		tag := map[Status]string{OK: " ok ", Warn: "warn", Fail: "FAIL"}[x.Status]
		fmt.Fprintf(w, "[%s] %s: %s\n", tag, x.Name, x.Detail)
		if x.Status != OK && x.Hint != "" {
			for _, line := range strings.Split(x.Hint, "\n") {
				fmt.Fprintf(w, "       %s\n", line)
			}
		}
	}
}

// FS is the part of the filesystem the checks need.
type FS interface {
	Glob(pattern string) ([]string, error)
	// OpenRW tries to open path for reading and writing, as ddcutil does.
	OpenRW(path string) error
}

// OSFS is the real filesystem.
type OSFS struct{}

// Glob implements FS.
func (OSFS) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }

// OpenRW implements FS.
func (OSFS) OpenRW(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

// Env holds what the checks run against.
type Env struct {
	DDC       ddc.Runner // runs ddcutil
	Gsettings ddc.Runner // runs gsettings; nil means unavailable
	FS        FS
}

// Check runs every check. It never writes anything.
func Check(ctx context.Context, e Env) Report {
	var rep Report
	add := func(r Result) { rep.Results = append(rep.Results, r) }

	// ddcutil
	if out, err := e.DDC.Run(ctx, "--version"); err != nil {
		add(Result{"ddcutil", Fail, firstLine(err.Error()),
			"Install it with your package manager, for example:\n" +
				"  Debian/Ubuntu: sudo apt install ddcutil\n" +
				"  Fedora:        sudo dnf install ddcutil\n" +
				"  Arch:          sudo pacman -S ddcutil"})
	} else {
		add(Result{Name: "ddcutil", Status: OK, Detail: firstLine(out)})
	}

	// i2c devices and access
	devs, _ := e.FS.Glob("/dev/i2c-*")
	if len(devs) == 0 {
		add(Result{"i2c devices", Fail, "no /dev/i2c-* devices",
			"Load the kernel module: sudo modprobe i2c-dev\n" +
				"and make it permanent: echo i2c-dev | sudo tee /etc/modules-load.d/i2c-dev.conf"})
	} else {
		add(Result{Name: "i2c devices", Status: OK, Detail: fmt.Sprintf("%d found", len(devs))})
		denied, usable := 0, 0
		var lastErr error
		for _, d := range devs {
			if err := e.FS.OpenRW(d); err != nil {
				lastErr = err
				denied++
			} else {
				usable++
			}
		}
		switch {
		case usable == 0:
			hint := "Check the permissions of /dev/i2c-*."
			if errors.Is(lastErr, fs.ErrPermission) {
				hint = "Your user cannot open the i2c devices. Add it to the i2c group and log in again:\n" +
					"  sudo usermod -aG i2c $USER\n" +
					"(or install ddcutil's udev rules; see its documentation)"
			}
			add(Result{"i2c access", Fail, firstLine(lastErr.Error()), hint})
		case denied > 0:
			add(Result{"i2c access", Warn, fmt.Sprintf("%d of %d devices are not accessible", denied, len(devs)),
				"Monitors on those buses will not be controllable until the permissions are fixed."})
		default:
			add(Result{Name: "i2c access", Status: OK, Detail: "read/write allowed"})
		}
	}

	// night-light backend (optional)
	if e.Gsettings == nil {
		add(Result{"night light", Warn, "gsettings not available",
			"Night mode will use DDC color control only."})
	} else if _, err := nightlight.Detect(ctx, e.Gsettings); err != nil {
		add(Result{"night light", Warn, "no supported desktop night light",
			"Night mode will use DDC color control only."})
	} else {
		rep.NightLight = true
		add(Result{Name: "night light", Status: OK, Detail: "GNOME night light available (fallback for monitors without color control)"})
	}
	return rep
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
