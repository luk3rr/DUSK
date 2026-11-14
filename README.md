# dusk

dusk schedules the brightness of every monitor and gives them a gentle, gradual night mode.
It talks to your monitors over DDC/CI (through `ddcutil`), so it changes the real monitor
settings, not just a software overlay, and it works per monitor.

- Detects all your DDC/CI monitors, whatever their model.
- Brightness follows a schedule you set per monitor (for example 08:00 -> 70%, 22:00 -> 25%)
  and moves in steps of one point, so you never notice a jump.
- Night mode reduces the blue gain of each monitor at the times you choose, fading in and out
  over a ramp (30 minutes by default). Monitors without color control can use the desktop's night
  light instead (GNOME today).
- Runs as a systemd user service that starts at every login.
- A guided `dusk setup` writes the configuration for you.

## Requirements

- Linux with systemd (for the service; `dusk run` works without it).
- [`ddcutil`](https://www.ddcutil.com/) and read/write access to `/dev/i2c-*`.
- Monitors with DDC/CI enabled (see Troubleshooting).
- Go 1.26 or newer and [`just`](https://github.com/casey/just) to build from source.

## Install

```sh
git clone <this repository> && cd <this repository>
just install        # builds ~/.local/bin/dusk and runs `dusk install`
```

`dusk install` offers to run the setup wizard if you have no configuration yet, shows exactly what
it is going to do (the unit file it writes and the `systemctl --user` commands it runs), and asks
for confirmation before changing anything. Make sure `~/.local/bin` is in your `PATH`.

To only try it without installing the service:

```sh
just build
./bin/dusk setup    # creates ~/.config/dusk/config.toml
./bin/dusk run      # runs in this terminal; Ctrl+C stops it
```

## Setup

`dusk setup` checks that `ddcutil` and the I2C devices work, lists your monitors, and asks for each
one a name and a brightness schedule (a day/evening/night preset, a constant value or your own key
points). It then asks about night mode (start, end, fade duration, how much blue to remove), shows
the resulting file and writes it after you confirm. An existing file is backed up as
`config.toml.bak-<timestamp>` and its values become the defaults, so you can run the wizard again
at any time. `dusk setup --yes` accepts every default without asking. The wizard only reads from
your monitors; it never changes them.

## Commands

| Command | What it does |
|---|---|
| `dusk setup [--yes] [--config file]` | Detect monitors and create the configuration interactively |
| `dusk install [--yes] [--no-start]` | Install and start the systemd user service |
| `dusk uninstall [--yes]` | Stop and remove the service (the configuration is kept) |
| `dusk run [--config file]` | Run the daemon in the foreground |
| `dusk status` | Show whether dusk is running or paused, the night-mode level and each monitor |
| `dusk logs [-f] [-n N] [--since TIME]` | Show the service log (`-f` follows it; it wraps `journalctl --user -u dusk.service`) |
| `dusk pause` / `dusk resume` | Freeze or resume every brightness and color change |
| `dusk reload` | Re-read the configuration file (an invalid file is rejected and the old one stays) |
| `dusk start` | Start the installed service again (the counterpart of `dusk stop`); `dusk run` is only for the foreground |
| `dusk stop` | Stop the daemon; the monitors' original colors are restored |
| `dusk list` | List the detected monitors |
| `dusk check-config [file]` | Validate a configuration file |

Control commands exit with 0 on success, 1 on an error and 2 when the daemon is not running.

## Configuration

The file lives at `~/.config/dusk/config.toml` (or `$XDG_CONFIG_HOME/dusk/config.toml`).

```toml
poll_interval   = "30s"   # how often dusk re-checks the targets
rescan_interval = "5m"    # how often it looks for new or returning monitors

[night]
enabled    = true
start      = "21:00"      # the fade in starts here...
end        = "07:00"      # ...and the fade out starts here
ramp       = "30m"        # how long each fade takes
blue_gain  = 60           # percent of the original blue at the peak (lower is warmer)
red_gain   = 100
green_gain = 100
fallback   = "auto"       # "auto": use the desktop night light for monitors without color control

[[monitor]]
name  = "desk"
match = "MODEL-27#SERIAL1" # "model" or "model#serial", as shown by `dusk list`
night = { blue_gain = 45 } # optional: this monitor's own night color level
points = [
  { time = "08:00", brightness = 70 },
  { time = "18:00", brightness = 50 },
  { time = "22:00", brightness = 25 },
]
```

- Between two key points the brightness changes linearly and continuously. After the last point of
  the day it keeps moving toward the first point of the next day.
- A monitor is matched by its model, optionally with its serial number, so changing the cable to
  another port does not break the configuration. Two identical models without a serial number
  share one entry.
- Monitors that are not listed are never touched.
- Every monitor can have its own night color level, because panels differ. Add
  `night = { blue_gain = 45 }` to its `[[monitor]]` (and, if you want, `red_gain` and `green_gain`).
  A value you leave out is inherited from `[night]`. The window (`start`, `end`), the `ramp` and the
  `fallback` always stay global so every screen warms up together. `dusk setup` asks about this
  for each monitor, and `dusk reload` applies a change you made by hand.

### How night mode works

At the peak, each monitor's blue gain is set to `blue_gain` percent of the value it had before dusk
touched it. The original values are saved in `~/.local/state/dusk/state.json` before the first
change, so a crash in the middle of the night cannot leave a monitor tinted forever: the next start
puts the colors back. Stopping the service restores the original colors immediately.

If a monitor does not support color control and `fallback = "auto"`, dusk uses the desktop night
light (GNOME) for the whole screen, at the temperature you already had configured there, and
halves the DDC effect on the other monitors so the two do not add up. Your own night-light
settings are saved and restored afterward. Without a supported night light, those monitors simply
get no night mode and dusk logs a warning once.

## Troubleshooting

- **No monitors found.** Enable DDC/CI in the monitor's on-screen menu. Some docks, KVMs and
  adapters do not pass DDC. `ddcutil detect` should list your monitors.
- **Permission denied on `/dev/i2c-*`.** Add yourself to the `i2c` group (`sudo usermod -aG i2c $USER`)
  and log in again, or install the udev rules that ship with `ddcutil`. If there are no
  `/dev/i2c-*` devices, run `sudo modprobe i2c-dev`.
- **A monitor is slow to react.** DDC is slow by design (about a second per command), which is why
  dusk changes one point at a time and waits at least 2 seconds between writes to the same monitor.
- **See what it is doing.** `dusk status` and `dusk logs -f`.
- **Manual change on the monitor.** The schedule wins: dusk moves it back toward the target at the
  next check. Use `dusk pause` when you want full manual control.

## Development

```sh
just check          # the official verification: gofmt, go vet and go test -race
just english-check  # the repository is written in English: fails on non-ASCII in the sources
just build | test | fmt | vet | run <args>
```

Code layout (`internal/`):

| Package | Role |
|---|---|
| `config` | TOML parsing, validation and generation |
| `schedule` | Pure functions: target brightness, night intensity, stepping |
| `ddc`, `monitors` | `ddcutil` adapter with a write whitelist, monitor discovery and matching |
| `nightlight`, `state` | Desktop night-light backend (GNOME) and persisted originals |
| `controller` | The loop that applies the schedule, handles failures and shutdown |
| `ipc`, `daemon` | Control socket and the glue behind `dusk run` |
| `prompt`, `preflight`, `setup` | The setup wizard |
| `install` | The systemd user service |

Tests never touch real monitors or the desktop: every external effect goes through an interface
with a fake in `internal/ddc/ddctest`. Writes to monitors are restricted to brightness (VCP 0x10)
and the red, green and blue gains (0x16, 0x18, 0x1A).
