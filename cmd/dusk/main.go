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
	"syscall"
	"text/tabwriter"
	"time"

	"dusk/internal/config"
	"dusk/internal/daemon"
	"dusk/internal/ddc"
	"dusk/internal/install"
	"dusk/internal/ipc"
	"dusk/internal/logs"
	"dusk/internal/monitors"
	"dusk/internal/preflight"
	"dusk/internal/prompt"
	"dusk/internal/setup"
	"dusk/internal/state"
)

// Seams that tests replace so they never touch the real machine.
var (
	setupFS         preflight.FS = preflight.OSFS{}
	setupIn         io.Reader    = os.Stdin
	gsettingsRunner ddc.Runner   = ddc.ExecRunner{Path: "gsettings"}
	systemctl       ddc.Runner   = ddc.ExecRunner{Path: "systemctl"}
	executable                   = os.Executable
	startWait                    = 5 * time.Second // how long `dusk start` waits for the daemon to answer
	journalctl      logs.Exec    = logs.ExecJournalctl
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
		return runSetup(ctx, args[1:], r, stdout, stderr)
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

const usage = `usage: dusk <command>

commands:
  setup [--yes]         detect the monitors and create the configuration interactively
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
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	if len(displays) == 0 {
		fmt.Fprintln(stderr, "no DDC/CI monitors found")
		return exitUsage
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
		fmt.Fprintf(stderr, "configuration file not found: %s\ncreate it with `dusk setup`\n", path)
		return exitUsage
	}
	sock, err := ipc.SocketPath()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	statePath, err := state.DefaultPath()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	err = daemon.Run(ctx, daemon.Params{
		ConfigPath: path,
		SocketPath: sock,
		StatePath:  statePath,
		DDC:        r,
		Gsettings:  gsettingsRunner,
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
		state = "paused"
	}
	fmt.Fprintf(w, "state: %s\nnight mode: %.0f%%\n", state, st.Night*100)
	if len(st.Monitors) == 0 {
		fmt.Fprintln(w, "no monitors under a schedule yet")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tMONITOR\tBUS\tCURRENT\tTARGET\tSTATUS\tLAST ERROR")
	for _, m := range st.Monitors {
		status := "online"
		if !m.Online {
			status = "offline"
		}
		last := m.LastError
		if last == "" {
			last = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\t%s\n", m.Name, m.Key, m.Bus, m.Current, m.Target, status, last)
	}
	tw.Flush()
}

// runSetup implements `dusk setup`.
func runSetup(ctx context.Context, args []string, r ddc.Runner, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "configuration file to write (default: the user config directory)")
	yes := fs.Bool("yes", false, "accept every default without asking")
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
	err := setup.Run(ctx, setup.Options{
		ConfigPath: path,
		Yes:        *yes,
		DDC:        r,
		Gsettings:  gsettingsRunner,
		FS:         setupFS,
		In:         setupIn,
		Out:        stdout,
	})
	if err != nil {
		fmt.Fprintln(stderr, "\ndusk setup:", err)
		return exitUsage
	}
	return exitOK
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
		In:             setupIn,
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
	o.RunSetup = func(ctx context.Context, p *prompt.Prompter) error {
		return setup.Run(ctx, setup.Options{
			ConfigPath: o.ConfigPath, Yes: o.Yes, DDC: r, Gsettings: gsettingsRunner, FS: setupFS, Out: stdout, Prompter: p,
		})
	}
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
	if _, err := systemctl.Run(ctx, "--user", "start", install.UnitName); err != nil {
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
