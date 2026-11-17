// Package install sets dusk up as a systemd user service and removes it again.
// It is the only part of dusk that changes the machine outside its own files,
// so every step is confirmed first and every command goes through a Runner.
package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dusk/internal/ddc"
	"dusk/internal/prompt"
)

// UnitName is the systemd unit dusk installs.
const UnitName = "dusk.service"

// ErrDeclined means the user did not confirm and nothing was changed.
var ErrDeclined = errors.New("nothing was changed: the installation was not confirmed")

// Options are the inputs of Install and Uninstall.
type Options struct {
	Bin        string // absolute path of the dusk executable (see ResolveBin)
	ConfigPath string // the configuration file the service will use
	// ExplicitConfig is true when ConfigPath is not the default one and must be
	// passed to `dusk run` with --config.
	ExplicitConfig bool
	UnitDir        string     // normally ~/.config/systemd/user
	Systemctl      ddc.Runner // runs systemctl
	Yes            bool
	NoStart        bool
	// RunSetup creates the configuration when it does not exist yet. It receives
	// the prompter so it shares the input stream with Install.
	RunSetup func(ctx context.Context, p *prompt.Prompter) error
	In       io.Reader
	Out      io.Writer
}

// UnitPath returns where the unit file lives.
func (o Options) UnitPath() string { return filepath.Join(o.UnitDir, UnitName) }

// ResolveBin returns the absolute, symlink-free path of the dusk executable.
// override is used instead of exe when not empty. Temporary `go run` builds are
// refused, because the service would break as soon as they are cleaned up.
func ResolveBin(exe, override string) (string, error) {
	path := exe
	if override != "" {
		path = override
	}
	if path == "" {
		return "", errors.New("cannot tell where the dusk executable is; pass --bin <path>")
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		path = abs
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("the executable %s cannot be used: %w", path, err)
	}
	if info, err := os.Stat(resolved); err != nil || info.IsDir() {
		return "", fmt.Errorf("%s is not an executable file", resolved)
	}
	if strings.Contains(resolved, "go-build") {
		return "", errors.New("this is a temporary `go run` build and cannot be used by a service;\n" +
			"build and install a real binary first (`just install`), or pass the path of a stable one with --bin")
	}
	return resolved, nil
}

// RenderUnit returns the systemd unit text. configPath is added to ExecStart
// as --config only when it is not empty.
func RenderUnit(bin, configPath string) string {
	exec := quote(bin) + " run"
	if configPath != "" {
		exec += " --config " + quote(configPath)
	}
	return "[Unit]\n" +
		"Description=dusk: monitor brightness schedule and night mode\n" +
		"After=graphical-session.target\n" +
		"PartOf=graphical-session.target\n" +
		"\n" +
		"[Service]\n" +
		"Type=simple\n" +
		"ExecStart=" + exec + "\n" +
		"ExecStopPost=" + quote(bin) + " reset-gamma\n" +
		"Restart=on-failure\n" +
		"RestartSec=5\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=graphical-session.target\n"
}

// quote makes s safe as an argument in a systemd ExecStart line.
func quote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// Install writes, enables and (unless NoStart) starts the user service.
func Install(ctx context.Context, o Options) error {
	p := prompt.New(o.In, o.Out, o.Yes)

	if _, err := os.Stat(o.ConfigPath); errors.Is(err, os.ErrNotExist) {
		p.Say("There is no configuration file yet (%s).", o.ConfigPath)
		if o.RunSetup == nil {
			return errors.New("create the configuration first with `dusk setup`")
		}
		ok, err := p.Confirm("Run the setup wizard now?", true)
		if err != nil {
			return err
		}
		if !ok {
			return ErrDeclined
		}
		if err := o.RunSetup(ctx, p); err != nil {
			return err
		}
		p.Say("")
	}

	cfgArg := ""
	if o.ExplicitConfig {
		cfgArg = o.ConfigPath
	}
	unit := RenderUnit(o.Bin, cfgArg)
	enable := []string{"--user", "enable", "--now", UnitName}
	if o.NoStart {
		enable = []string{"--user", "enable", UnitName}
	}

	p.Say("This will:")
	p.Say("  1. write %s", o.UnitPath())
	p.Say("       ExecStart=%s", execLine(unit))
	p.Say("  2. run: systemctl --user daemon-reload")
	p.Say("  3. run: systemctl %s", strings.Join(enable, " "))
	p.Say("The service starts at every graphical login and restarts itself if it crashes.")
	ok, err := p.Confirm("Install it?", true)
	if err != nil {
		return err
	}
	if !ok {
		return ErrDeclined
	}

	if err := os.MkdirAll(o.UnitDir, 0o755); err != nil {
		return err
	}
	if err := writeAtomic(o.UnitPath(), []byte(unit)); err != nil {
		return err
	}
	p.Say("Wrote %s", o.UnitPath())
	if _, err := o.Systemctl.Run(ctx, "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("the unit was written, but systemd did not reload: %w\n%s", err, systemdHint)
	}
	if _, err := o.Systemctl.Run(ctx, enable...); err != nil {
		return fmt.Errorf("the unit was written, but enabling it failed: %w\n%s", err, systemdHint)
	}
	if o.NoStart {
		p.Say("\nThe service is enabled and will start at the next login. Start it now with: systemctl --user start %s", UnitName)
	} else {
		p.Say("\nThe service is enabled and running.")
	}
	p.Say("Check it:    dusk status")
	p.Say("Logs:        dusk logs -f")
	p.Say("Pause it:    dusk pause   (and dusk resume)")
	p.Say("Remove it:   dusk uninstall")
	return nil
}

const systemdHint = "Is the systemd user manager running? Try: systemctl --user status"

// Uninstall stops and disables the service and removes its unit file. The
// configuration and state files are kept.
func Uninstall(ctx context.Context, o Options, keep []string) error {
	p := prompt.New(o.In, o.Out, o.Yes)
	p.Say("This will stop and disable %s (the monitors' original colors are restored when it stops) and delete %s.", UnitName, o.UnitPath())
	ok, err := p.Confirm("Uninstall it?", true)
	if err != nil {
		return err
	}
	if !ok {
		return ErrDeclined
	}
	if _, err := o.Systemctl.Run(ctx, "--user", "disable", "--now", UnitName); err != nil {
		p.Say("note: could not disable the unit (it may not be installed): %v", err)
	}
	if err := os.Remove(o.UnitPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := o.Systemctl.Run(ctx, "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("the unit file was removed, but systemd did not reload: %w\n%s", err, systemdHint)
	}
	p.Say("Uninstalled.")
	if len(keep) > 0 {
		p.Say("These files were kept; delete them yourself if you no longer want them:")
		for _, k := range keep {
			p.Say("  %s", k)
		}
	}
	return nil
}

// execLine returns the ExecStart value of a rendered unit.
func execLine(unit string) string {
	for _, l := range strings.Split(unit, "\n") {
		if v, ok := strings.CutPrefix(l, "ExecStart="); ok {
			return v
		}
	}
	return ""
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".unit-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
