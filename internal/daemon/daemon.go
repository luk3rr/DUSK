// Package daemon wires the configuration, the controller and the control
// socket together into the long-running `dusk run` process.
package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"dusk/internal/config"
	"dusk/internal/controller"
	"dusk/internal/ddc"
	"dusk/internal/gamma"
	"dusk/internal/ipc"
	"dusk/internal/monitors"
)

// Params are the daemon's inputs. Everything that touches the machine is
// injectable so tests can run without hardware.
type Params struct {
	ConfigPath string
	SocketPath string
	DDC        ddc.Runner    // runs ddcutil (only used by monitors with brightness = "ddc")
	Gamma      gamma.Backend // software color; nil means no backend
	Log        *slog.Logger
}

// Daemon answers control requests for a running Controller.
type Daemon struct {
	ConfigPath string
	Ctl        *controller.Controller
	LoadConfig func(path string) (*config.Config, error)
	Stop       context.CancelFunc
	Log        *slog.Logger
}

// Handle implements ipc.Handler.
func (d *Daemon) Handle(_ context.Context, req ipc.Request) (ipc.Response, func()) {
	switch req.Cmd {
	case ipc.CmdStatus:
		return ipc.Response{OK: true, Status: toIPC(d.Ctl.Status(), d.ConfigPath)}, nil
	case ipc.CmdPause:
		d.Ctl.Pause()
		return ipc.Response{OK: true}, nil
	case ipc.CmdResume:
		d.Ctl.Resume()
		return ipc.Response{OK: true}, nil
	case ipc.CmdReload:
		cfg, err := d.LoadConfig(d.ConfigPath)
		if err != nil {
			d.Log.Warn("reload rejected; keeping the current configuration", "error", err)
			return ipc.Response{Error: err.Error()}, nil
		}
		d.Ctl.SetConfig(cfg)
		d.Ctl.Wake()
		d.Log.Info("configuration reloaded", "monitors", len(cfg.Monitors))
		return ipc.Response{OK: true}, nil
	case ipc.CmdStop:
		d.Log.Info("stop requested")
		return ipc.Response{OK: true}, d.Stop
	default:
		return ipc.Response{Error: fmt.Sprintf("unknown command %q", req.Cmd)}, nil
	}
}

func toIPC(s controller.Status, configPath string) *ipc.Status {
	nm := s.NightMode
	night := ipc.NightStatus{Enabled: nm.Enabled, Ramp: config.FormatDuration(nm.Ramp), Phase: string(nm.Phase)}
	if nm.Enabled {
		night.Start, night.End = config.FormatClock(nm.Start), config.FormatClock(nm.End)
	}
	if !nm.NextChange.IsZero() {
		night.NextChange = nm.NextChange.Format("15:04")
	}
	out := &ipc.Status{
		Paused: s.Paused, Night: s.Night, NightMode: night, Config: configPath,
		PollInterval: config.FormatDuration(s.PollInterval), RescanInterval: config.FormatDuration(s.RescanInterval),
		VerifyInterval: config.FormatDuration(s.VerifyInterval),
		Warnings:       append([]string{}, s.Warnings...),
		Monitors:       []ipc.MonitorStatus{},
	}
	for _, m := range s.Monitors {
		ms := ipc.MonitorStatus{
			Name: m.Name, Key: m.Key, Output: m.Output, Bus: m.Bus, DDC: m.DDC,
			BrightnessBackend: m.Backend, Floor: m.Floor, Current: m.Current, Target: m.Target,
			Gamma: ipc.RGB{R: m.Gamma.R, G: m.Gamma.G, B: m.Gamma.B}, Scale: m.Scale,
			Online: m.Online, Fails: m.Fails, LastError: m.LastError,
			NightLevel: ipc.RGB{R: m.NightLevel.R, G: m.NightLevel.G, B: m.NightLevel.B}, OwnLevel: m.OwnLevel,
			Schedule: []ipc.SchedulePoint{},
		}
		for _, p := range m.Schedule {
			ms.Schedule = append(ms.Schedule, ipc.SchedulePoint{Time: config.FormatClock(p.Minute), Brightness: p.Brightness})
		}
		out.Monitors = append(out.Monitors, ms)
	}
	return out
}

// Run loads the configuration, serves the control socket and runs the
// controller until ctx is canceled or a stop command arrives. On the way out
// the controller puts the screens back to their normal colors.
func Run(ctx context.Context, p Params) error {
	log := p.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	cfg, err := config.Load(p.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading %s: %w", p.ConfigPath, err)
	}
	ln, err := ipc.Listen(p.SocketPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ctl := controller.New(controller.Options{
		Config: cfg,
		Gamma:  p.Gamma,
		Discover: func(ctx context.Context) ([]ddc.Display, error) {
			return monitors.Discover(ctx, p.DDC)
		},
		Log: log,
	})
	d := &Daemon{ConfigPath: p.ConfigPath, Ctl: ctl, LoadConfig: config.Load, Stop: cancel, Log: log}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := ipc.Serve(ctx, ln, d, log); err != nil {
			log.Error("control socket stopped", "error", err)
			cancel()
		}
	}()
	log.Info("dusk started", "config", p.ConfigPath, "socket", p.SocketPath)
	err = ctl.Run(ctx)
	cancel()
	wg.Wait()
	log.Info("dusk stopped")
	return err
}
