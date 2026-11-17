// Command dusk schedules monitor brightness and night mode over DDC/CI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"dusk/internal/config"
	"dusk/internal/daemon"
	"dusk/internal/ddc"
	"dusk/internal/gamma"
	"dusk/internal/install"
	"dusk/internal/ipc"
	"dusk/internal/logs"
	"dusk/internal/monitors"
	"dusk/internal/service"
	"dusk/internal/state"
)

// Seams that tests replace so they never touch the real machine.
var (
	stdin      io.Reader  = os.Stdin
	systemctl  ddc.Runner = ddc.ExecRunner{Path: "systemctl"}
	executable            = os.Executable
	startWait             = 5 * time.Second // how long `dusk start` waits for the daemon to answer
	journalctl logs.Exec  = logs.ExecJournalctl
)

// Exit codes.
const (
	exitOK       = 0
	exitUsage    = 1 // bad usage, invalid config or failed command
	exitNoDaemon = 2 // the daemon is not reachable
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], ddc.ExecRunner{}, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, r ddc.Runner, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		if interactive() {
			return runTUI(ctx, r, stderr)
		}
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "check-config":
		return checkConfig(args[1:], stdout, stderr)
	case "list":
		return list(ctx, r, stdout, stderr)
	case "run":
		return runDaemon(ctx, args[1:], r, stderr)
	case "setup":
		return runTUI(ctx, r, stderr) // the guided setup is now part of the terminal interface
	case "reset-ddc":
		return runResetDDC(ctx, args[1:], r, stdout, stderr)
	case "reset-gamma":
		return runResetGamma(ctx, stdout, stderr)
	case "start":
		return runStart(ctx, stdout, stderr)
	case "logs":
		return runLogs(ctx, args[1:], stdout, stderr)
	case "install":
		return runInstall(ctx, args[1:], r, stdout, stderr)
	case "uninstall":
		return runUninstall(ctx, args[1:], stdout, stderr)
	case ipc.CmdStatus, ipc.CmdPause, ipc.CmdResume, ipc.CmdReload, ipc.CmdStop:
		return control(ctx, args[0], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %q\n%s\n", args[0], usage)
		return exitUsage
	}
}

const usage = `usage: dusk [command]

With no command, dusk opens its terminal interface.

commands:
  setup                 open the terminal interface (creates the configuration if there is none)
  install [--yes]       install and start the systemd user service (starts at every login)
  uninstall [--yes]     stop and remove the service (the configuration is kept)
  run [--config file]   run the daemon (normally started by the service)
  start                 start the installed service (the counterpart of stop)
  status                show the daemon state and each monitor
  logs [-f] [-n N]      show the service log (-f follows it, -n sets how many lines)
  pause | resume        freeze or resume all brightness and color changes
  reload                re-read the configuration file
  stop                  stop the daemon, restoring the original colors
  list                  list the detected monitors
  reset-gamma           put every screen back to its normal colors and brightness
  reset-ddc [--yes] [--to-max]  restore the color gains an older dusk saved (asks first)
  check-config [file]   validate a configuration file`

func defaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dusk", "config.toml"), nil
}

func checkConfig(args []string, stdout, stderr io.Writer) int {
	var path string
	if len(args) > 0 {
		path = args[0]
	} else {
		p, err := defaultConfigPath()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		path = p
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", path, err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "%s: ok (%d monitor(s), night mode %v)\n", path, len(cfg.Monitors), cfg.Night.Enabled)
	for _, w := range cfg.Warnings {
		fmt.Fprintf(stdout, "warning: %s\n", w)
	}
	return exitOK
}

// list shows the detected monitors. It only reads from the hardware, never writes.
func list(ctx context.Context, r ddc.Runner, stdout, stderr io.Writer) int {
	var cfg *config.Config
	if path, err := defaultConfigPath(); err == nil {
		c, err := config.Load(path)
		switch {
		case err == nil:
			cfg = c
		case !os.IsNotExist(err):
			fmt.Fprintf(stderr, "warning: ignoring config %s: %v\n", path, err)
		}
	}
	displays, err := monitors.Discover(ctx, r)
	if err != nil {
		fmt.Fprintf(stderr, "note: DDC/CI monitors unavailable: %v\n", err)
		displays = nil
	}
	var outputs []gamma.Output
	if b, err := gammaConnect(ctx); err != nil {
		fmt.Fprintf(stderr, "note: no software color backend: %v\n", err)
	} else if outputs, err = b.Outputs(ctx); err != nil {
		fmt.Fprintf(stderr, "note: cannot list screens: %v\n", err)
	}
	if len(displays) == 0 && len(outputs) == 0 {
		fmt.Fprintln(stderr, "no monitors found (neither software-controllable screens nor DDC/CI monitors)")
		return exitUsage
	}
	if len(outputs) > 0 {
		if code := listScreens(stdout, stderr, cfg, outputs, displays); code != exitOK {
			return code
		}
	}
	if len(displays) == 0 {
		return exitOK
	}
	if len(outputs) > 0 {
		fmt.Fprintln(stdout)
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tMODEL\tSERIAL\tPORT\tBUS\tBRIGHTNESS\tRGB\tSUGGESTED MATCH")
	for _, a := range monitors.Match(cfg, displays) {
		info, caps := a.Display.Info(), a.Display.Caps()
		name := "-"
		if a.Monitor != nil {
			name = a.Monitor.Name
		}
		serial := info.Serial
		if serial == "" {
			serial = "-"
		}
		brightness := "no response"
		if caps.Brightness {
			if cur, max, err := a.Display.Get(ctx, ddc.VCPBrightness); err == nil {
				brightness = strconv.Itoa(cur) + "/" + strconv.Itoa(max)
			}
		}
		rgb := "no"
		if caps.RGB {
			rgb = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n", name, info.Model, serial, info.Connector, info.Bus, brightness, rgb, info.Key())
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	return exitOK
}

// listScreens prints the screens that accept software color, with their
// configuration entry and DDC bus.
func listScreens(stdout, stderr io.Writer, cfg *config.Config, outputs []gamma.Output, displays []ddc.Display) int {
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSCREEN\tMODEL\tSERIAL\tDDC BUS\tSUGGESTED MATCH")
	for _, r := range monitors.Resolve(cfg, outputs, displays) {
		name, serial, bus := "-", r.Output.Serial, "-"
		if r.Monitor != nil {
			name = r.Monitor.Name
		}
		if serial == "" {
			serial = "-"
		}
		if r.Display != nil {
			bus = strconv.Itoa(r.Display.Info().Bus)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", name, r.Output.Name, r.Output.Product, serial, bus, r.Output.Key())
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	return exitOK
}

// runDaemon implements `dusk run`.
func runDaemon(ctx context.Context, args []string, r ddc.Runner, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "configuration file (default: the user config directory)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	path := *cfgPath
	if path == "" {
		p, err := defaultConfigPath()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		path = p
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "configuration file not found: %s\ncreate it by running `dusk` (the Monitors tab creates it for you)\n", path)
		return exitUsage
	}
	sock, err := ipc.SocketPath()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	err = daemon.Run(ctx, daemon.Params{
		ConfigPath: path,
		SocketPath: sock,
		DDC:        r,
		Gamma:      gamma.Lazy(gammaConnect),
		Log:        slog.New(slog.NewTextHandler(stderr, nil)),
	})
	if err != nil {
		fmt.Fprintln(stderr, "dusk:", err)
		return exitUsage
	}
	return exitOK
}

// control sends a command to the running daemon and prints the result.
func control(ctx context.Context, cmd string, stdout, stderr io.Writer) int {
	sock, err := ipc.SocketPath()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	resp, err := ipc.Send(ctx, sock, ipc.Request{Cmd: cmd})
	if errors.Is(err, ipc.ErrNoDaemon) {
		fmt.Fprintln(stderr, "dusk is not running (start it with: dusk start)")
		return exitNoDaemon
	}
	if err != nil {
		fmt.Fprintln(stderr, "dusk:", err)
		return exitUsage
	}
	if !resp.OK {
		fmt.Fprintln(stderr, "dusk:", resp.Error)
		return exitUsage
	}
	if cmd == ipc.CmdStatus && resp.Status != nil {
		printStatus(stdout, resp.Status)
		return exitOK
	}
	fmt.Fprintln(stdout, "ok")
	return exitOK
}

func printStatus(w io.Writer, st *ipc.Status) {
	state := "running"
	if st.Paused {
		state = "paused (screens are back to their normal colors)"
	}
	fmt.Fprintf(w, "state:       %s\n", state)
	if st.Config != "" {
		fmt.Fprintf(w, "config:      %s\n", st.Config)
		fmt.Fprintf(w, "timing:      targets every %s, screen colors checked every minute, DDC monitors re-read every %s, screen list rescanned every %s\n", st.PollInterval, st.VerifyInterval, st.RescanInterval)
	}
	n := st.NightMode
	if !n.Enabled {
		fmt.Fprintln(w, "night mode:  off")
	} else {
		fmt.Fprintf(w, "night mode:  on, %s -> %s, fade %s\n", n.Start, n.End, n.Ramp)
		next := ""
		if n.NextChange != "" {
			next = ", next change at " + n.NextChange
		}
		fmt.Fprintf(w, "now:         %s (%.0f%%)%s\n", n.Phase, st.Night*100, next)
	}
	for _, warn := range st.Warnings {
		fmt.Fprintf(w, "warning:     %s\n", warn)
	}
	if len(st.Monitors) == 0 {
		fmt.Fprintln(w, "\nno screens under a schedule yet")
		return
	}
	for _, m := range st.Monitors {
		status := "online"
		if !m.Online {
			status = "offline"
		}
		ddcText := "no DDC"
		if m.DDC {
			ddcText = fmt.Sprintf("DDC bus %d", m.Bus)
		}
		fmt.Fprintf(w, "\n%s  (%s, %s, %s)  %s\n", m.Name, m.Key, m.Output, ddcText, status)
		if m.BrightnessBackend == config.BrightnessDDC {
			fmt.Fprintf(w, "  brightness   %d%% now, target %d%% (monitor backlight over DDC)\n", m.Current, m.Target)
		} else {
			fmt.Fprintf(w, "  brightness   %d%% now, target %d%% (software, floor %d%%, screen scale %d%%)\n", m.Current, m.Target, m.Floor, m.Scale)
		}
		fmt.Fprintf(w, "  color        R %d%%  G %d%%  B %d%% (software, about %d K)\n", m.Gamma.R, m.Gamma.G, m.Gamma.B,
			config.KelvinForGains(m.Gamma.R, m.Gamma.G, m.Gamma.B))
		if n.Enabled {
			level := "global level"
			if m.OwnLevel {
				level = "own level"
			}
			fmt.Fprintf(w, "  night level  R %d%%  G %d%%  B %d%% at the peak (%s)\n", m.NightLevel.R, m.NightLevel.G, m.NightLevel.B, level)
		}
		if len(m.Schedule) > 0 {
			parts := make([]string, 0, len(m.Schedule))
			for _, p := range m.Schedule {
				parts = append(parts, fmt.Sprintf("%s %d%%", p.Time, p.Brightness))
			}
			fmt.Fprintf(w, "  schedule     %s\n", strings.Join(parts, ", "))
		}
		if m.LastError != "" {
			fmt.Fprintf(w, "  last error   %s (%d failures)\n", m.LastError, m.Fails)
		}
	}
}

// installOptions builds the options shared by install and uninstall.
func installOptions(fs *flag.FlagSet, args []string) (install.Options, string, bool) {
	cfgPath := fs.String("config", "", "configuration file the service uses (default: the user config directory)")
	bin := fs.String("bin", "", "path of the dusk executable the service runs (default: this one)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return install.Options{}, "", false
	}
	def, err := defaultConfigPath()
	if err != nil {
		fmt.Fprintln(fs.Output(), err)
		return install.Options{}, "", false
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(fs.Output(), err)
		return install.Options{}, "", false
	}
	o := install.Options{
		ConfigPath:     def,
		ExplicitConfig: false,
		UnitDir:        filepath.Join(cfgDir, "systemd", "user"),
		Systemctl:      systemctl,
		Yes:            *yes,
		In:             stdin,
	}
	if *cfgPath != "" {
		o.ConfigPath, o.ExplicitConfig = *cfgPath, *cfgPath != def
	}
	return o, *bin, true
}

// runInstall implements `dusk install`.
func runInstall(ctx context.Context, args []string, r ddc.Runner, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	noStart := fs.Bool("no-start", false, "enable the service but do not start it now")
	o, binFlag, ok := installOptions(fs, args)
	if !ok {
		return exitUsage
	}
	exe, _ := executable()
	bin, err := install.ResolveBin(exe, binFlag)
	if err != nil {
		fmt.Fprintln(stderr, "dusk install:", err)
		return exitUsage
	}
	o.Bin, o.NoStart, o.Out = bin, *noStart, stdout
	if err := install.Install(ctx, o); err != nil {
		fmt.Fprintln(stderr, "\ndusk install:", err)
		return exitUsage
	}
	return exitOK
}

// runUninstall implements `dusk uninstall`.
func runUninstall(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o, _, ok := installOptions(fs, args)
	if !ok {
		return exitUsage
	}
	o.Out = stdout
	keep := []string{o.ConfigPath}
	if sp, err := state.DefaultPath(); err == nil {
		keep = append(keep, sp)
	}
	if err := install.Uninstall(ctx, o, keep); err != nil {
		fmt.Fprintln(stderr, "\ndusk uninstall:", err)
		return exitUsage
	}
	return exitOK
}

// runLogs implements `dusk logs`.
func runLogs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o logs.Options
	fs.BoolVar(&o.Follow, "f", false, "keep printing new lines until interrupted")
	fs.BoolVar(&o.Follow, "follow", false, "same as -f")
	fs.IntVar(&o.Lines, "n", logs.DefaultLines, "number of recent lines to show")
	fs.IntVar(&o.Lines, "lines", logs.DefaultLines, "same as -n")
	fs.StringVar(&o.Since, "since", "", `only show entries since this time, for example "1 hour ago" or "today"`)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if err := logs.Show(ctx, journalctl, o, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "dusk logs:", err)
		return exitUsage
	}
	return exitOK
}

// runStart implements `dusk start`: it starts the installed systemd user service
// and waits until the daemon answers.
func runStart(ctx context.Context, stdout, stderr io.Writer) int {
	sock, err := ipc.SocketPath()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	if _, err := ipc.Send(ctx, sock, ipc.Request{Cmd: ipc.CmdStatus}); err == nil {
		fmt.Fprintln(stdout, "dusk is already running")
		return exitOK
	}
	if err := (service.Manager{Systemctl: systemctl}).Start(ctx); err != nil {
		fmt.Fprintf(stderr, "dusk start: %v\nIs the service installed? Install it with: dusk install\n", err)
		return exitUsage
	}
	deadline := time.Now().Add(startWait)
	for {
		if _, err := ipc.Send(ctx, sock, ipc.Request{Cmd: ipc.CmdStatus}); err == nil {
			fmt.Fprintln(stdout, "dusk started")
			return exitOK
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			fmt.Fprintln(stderr, "dusk start: the service was started but the daemon is not answering yet; check: dusk logs")
			return exitUsage
		}
		time.Sleep(100 * time.Millisecond)
	}
}
