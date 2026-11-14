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
	"dusk/internal/ipc"
	"dusk/internal/monitors"
	"dusk/internal/nightlight"
	"dusk/internal/state"
)

// Params are the daemon's inputs. Everything that touches the machine is
// injectable so tests can run without hardware.
type Params struct {
	ConfigPath string
	SocketPath string
	StatePath  string
	DDC        ddc.Runner // runs ddcutil
	Gsettings  ddc.Runner // runs gsettings; nil means no night-light backend
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
	rgb := func(r *controller.RGB) *ipc.RGB {
		if r == nil {
			return nil
		}
		return &ipc.RGB{R: r.R, G: r.G, B: r.B}
	}
	nm := s.NightMode
	night := ipc.NightStatus{
		Enabled: nm.Enabled, Ramp: config.FormatDuration(nm.Ramp), Phase: string(nm.Phase),
		Fallback: nm.Fallback, FallbackActive: nm.FallbackActive, FallbackKelvin: nm.FallbackKelvin,
	}
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
		Monitors:       []ipc.MonitorStatus{},
	}
	for _, m := range s.Monitors {
		ms := ipc.MonitorStatus{
			Name: m.Name, Key: m.Key, Bus: m.Bus, Current: m.Current, Target: m.Target,
			Online: m.Online, Fails: m.Fails, LastError: m.LastError,
			ColorControl: m.ColorControl, RGB: rgb(m.RGB), RGBTarget: rgb(m.RGBTarget), RGBOriginal: rgb(m.RGBOriginal),
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
// the controller restores the monitors' original RGB gains and the night light.
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

	var night nightlight.Backend
	if p.Gsettings != nil {
		if b, err := nightlight.Detect(ctx, p.Gsettings); err != nil {
			log.Info("no night-light backend", "reason", err)
		} else {
			night = b
			log.Info("night-light backend detected: GNOME")
		}
	}

	ctl := controller.New(controller.Options{
		Config: cfg,
		Discover: func(ctx context.Context) ([]ddc.Display, error) {
			return monitors.Discover(ctx, p.DDC)
		},
		Night: night,
		Store: &state.Store{Path: p.StatePath},
		Log:   log,
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
