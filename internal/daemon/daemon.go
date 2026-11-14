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
		return ipc.Response{OK: true, Status: toIPC(d.Ctl.Status())}, nil
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

func toIPC(s controller.Status) *ipc.Status {
	out := &ipc.Status{Paused: s.Paused, Night: s.Night, Monitors: []ipc.MonitorStatus{}}
	for _, m := range s.Monitors {
		out.Monitors = append(out.Monitors, ipc.MonitorStatus{
			Name: m.Name, Key: m.Key, Bus: m.Bus, Current: m.Current, Target: m.Target,
			Online: m.Online, Fails: m.Fails, LastError: m.LastError,
		})
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
