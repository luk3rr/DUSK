# dusk

Per-monitor brightness schedules and a gradual night mode for Linux, with a terminal interface.

dusk changes the colors and brightness of each screen **in software** (a gamma ramp), so nothing is
left behind in the monitor. Turn the computer off at night and plug the monitor into another
machine the next day: it is exactly as you left it before dusk touched it.

- Brightness follows a schedule you set per monitor (for example 08:00 -> 70%, 22:00 -> 25%) and
  moves one point at a time, so you never notice a jump.
- Night mode reduces the blue (or red, or green) of each screen at the times you choose, fading in
  and out over a ramp (30 minutes by default). Each monitor can have its own color level, since
  panels differ.
- Everything is done from a full-screen terminal interface (`dusk`): create the configuration, edit
  monitors and schedules, change the night window, install and control the service, read the log.
  The command line is still there for scripts.
- Optionally a monitor can use its real backlight over DDC/CI (`brightness = "ddc"`). Only the
  brightness is written then, never the colors.
- Runs as a systemd user service that starts at every login.

Works on GNOME with Wayland (it uses Mutter's gamma control, one ramp per screen). KDE, wlroots
compositors and X11 are not supported yet. Tested on Ubuntu with an NVIDIA GPU and three monitors.

## Requirements

- GNOME on Wayland (Mutter) with a session D-Bus.
- Linux with systemd, for the service (`dusk run` works without it).
- Go 1.26 or newer and [`just`](https://github.com/casey/just) to build from source.
- Only for `brightness = "ddc"` and `dusk reset-ddc`: [`ddcutil`](https://www.ddcutil.com/), read/write
  access to `/dev/i2c-*` and monitors with DDC/CI enabled. Without them everything else works.

## Install

```sh
git clone https://github.com/luk3rr/DUSK.git
cd DUSK
just install        # builds ~/.local/bin/dusk and runs `dusk install`
```

Make sure `~/.local/bin` is in your `PATH`. `dusk install` needs a configuration, so run `dusk`
first: on the Monitors tab press `n` to create one with an entry for every screen it finds. Then
`just install`, or press `i` on the Service tab.

To only try it without installing the service:

```sh
just build
./bin/dusk          # create the configuration (Monitors tab, key n)
./bin/dusk run      # runs in this terminal; Ctrl+C stops it and restores the screens
```

## The terminal interface

Run `dusk` (or `dusk setup`) in a terminal. It needs at least 80x24.

| Tab | What you do there |
|---|---|
| 1 Status | Live view, refreshed every second: brightness now and target, the color applied to each screen, the night phase, warnings. `p` pauses or resumes, `r` reloads the configuration. |
| 2 Monitors | Select a monitor with the arrows, `Enter` to edit it, `a` to add one, `d` to delete it. The editor has the name, the match, the brightness backend (`software` or `ddc`), the floor, the night color (global, a warmth of its own, or custom RGB) and the schedule (`Ctrl+A` adds a point, `Ctrl+D` removes one). `Ctrl+S` saves, `Esc` cancels. |
| 3 Night | Turn night mode on or off and set the start, the end, the fade ramp and the color: a **warmth** slider in kelvin (6500 K = no change, lower is warmer, like GNOME's Night Light) or, if you prefer, three RGB sliders. A swatch shows the resulting color. |
| 4 Service | `i` install, `u` uninstall, `s` start, `x` stop, `l` the log, `d` restore the colors an older dusk left in the monitors. |

Percentages, the warmth, the fade ramp and the schedule levels are sliders: `Left`/`Right` move one
step, `Shift+Left`/`Shift+Right` (or `PgUp`/`PgDn`) a bigger one, `Home`/`End` jump to the ends. Times
(`HH:MM`) can be typed or moved with `Left`/`Right` (15 minutes) and `Shift+Left`/`Shift+Right` (1 hour).
Names and matches are the only fields you type.

Every field is checked as you save and the message appears under the field. Nothing invalid is ever
written: the file is validated, the old one is kept next to it as `config.toml.bak-<time>`, and the
new one replaces it atomically. The running daemon is told to reload. Saving from the interface
rewrites the file, so comments you wrote by hand are not kept (the backup has them).

## Commands

| Command | What it does |
|---|---|
| `dusk` | Open the terminal interface (`dusk setup` does the same) |
| `dusk install [--yes] [--no-start]` | Install and start the systemd user service |
| `dusk uninstall [--yes]` | Stop and remove the service (the configuration is kept) |
| `dusk start` / `dusk stop` | Start the installed service / stop the daemon (the screens go back to normal) |
| `dusk status` | Show whether dusk is running or paused, the night level and each monitor |
| `dusk pause` / `dusk resume` | Put the screens back to normal and freeze / apply the schedule again |
| `dusk reload` | Re-read the configuration (an invalid file is rejected and the old one stays) |
| `dusk logs [-f] [-n N] [--since TIME]` | Show the service log (`-f` follows it; needs systemd) |
| `dusk list` | List the screens, their configuration entry and DDC bus |
| `dusk check-config [file]` | Validate a configuration file |
| `dusk run [--config file]` | Run the daemon in the foreground (Ctrl+C stops it) |
| `dusk reset-gamma` | Put every screen back to its normal colors and brightness right now |
| `dusk reset-ddc [--yes] [--to-max]` | Restore the color gains an older dusk saved in the monitors (asks first) |

Control commands exit with 0 on success, 1 on an error and 2 when the daemon is not running.

## Configuration

The file lives at `~/.config/dusk/config.toml` (or `$XDG_CONFIG_HOME/dusk/config.toml`). The
interface writes it for you; you can still edit it by hand and run `dusk reload`.

```toml
poll_interval   = "30s"   # how often dusk re-checks the targets
rescan_interval = "30m"   # how often it looks for new or returning screens
verify_interval = "5m"    # DDC only: how often it re-reads a monitor's backlight

[night]
enabled    = true
start      = "21:00"      # the fade in starts here...
end        = "07:00"      # ...and the fade out starts here
ramp       = "30m"        # how long each fade takes
temperature = 3400        # color temperature at the peak, 1700-6500 K (6500 = no change, lower is warmer)
# Instead of a temperature you can set the gains of each channel, in percent of full:
# blue_gain = 60  /  red_gain = 100  /  green_gain = 100   (the temperature wins when both are present)

[[monitor]]
name       = "desk"
match      = "MODEL-27#SERIAL1" # "model" or "model#serial", as shown by `dusk list`
brightness = "software"         # "software" (default) or "ddc"
floor      = 10                 # lowest software brightness in percent (0-50)
night = { temperature = 3000 }  # optional: this monitor's own night color (or blue_gain/red_gain/green_gain)
points = [
  { time = "08:00", brightness = 70 },
  { time = "18:00", brightness = 50 },
  { time = "22:00", brightness = 25 },
]
```

- Between two key points the brightness changes linearly. After the last point of the day it keeps
  moving toward the first point of the next day.
- A screen is matched by its model, optionally with its serial number, so moving the cable to another
  port does not break the configuration. Screens that are not listed are never touched.
- Every monitor can have its own night color, a temperature or gains. What you leave out is inherited from
  `[night]`. The window and the ramp always stay global so every screen warms up together.
- An older `fallback = "auto"` key is still accepted but ignored (with a warning): the desktop night
  light is no longer used, every screen is handled by dusk itself.

## How it works

For each screen dusk builds a gamma ramp: the identity ramp multiplied by the night color gain of
each channel and by a brightness scale, and hands it to Mutter. The scale goes from the `floor` (at
0%) to 1 (at 100%), so the schedule can never turn a screen black unless you ask for `floor = 0`.

- Changes are applied one point at a time, 40 ms apart, so a transition is gentle. After `dusk
  resume`, or when the daemon starts, the screens fade from normal to the scheduled look.
- Every minute dusk reads the ramp back. If another program replaced it (a resume from sleep, a
  monitor plugged in, GNOME's own Night Light) dusk puts its ramp back; if that keeps happening the
  status shows a warning, usually because GNOME's Night Light is on.
- Stopping, pausing or killing the service puts the screens back to normal. The systemd unit also
  runs `dusk reset-gamma` after the daemon exits, so even a crash is covered. Logging out or
  rebooting resets the ramps anyway.
- Brightness by software lowers the picture's signal, not the backlight, so the contrast drops a
  little. If you prefer the real backlight on a monitor, set `brightness = "ddc"` for it; dusk then
  writes only the brightness over DDC/CI and restores the value it found when it stops.
- dusk uses its own ramp as a starting point: color profiles (ICC) that GNOME applies as a gamma
  table are replaced while dusk runs.

### Upgrading from an older dusk

Older versions wrote the night-mode color gains into the monitors over DDC/CI, and those values
stayed there when the computer was switched off. The new dusk never does that. If a monitor is
still tinted from the old behavior:

```sh
dusk reset-ddc          # shows each monitor and asks before writing anything
```

It restores the original values the old dusk saved in `~/.local/state/dusk/state.json`. For a monitor
with no saved original it does nothing, because only the monitor knows what "normal" is for it
(many are set below 100 on purpose); use `--to-max` only if you want 100%. The same action is on the
Service tab (`d`). Your existing `config.toml` keeps working as it is.

## Troubleshooting

- **"no software color backend".** dusk could not reach Mutter's DisplayConfig on the session
  D-Bus. It must run inside your graphical session (the service starts with it) on GNOME/Wayland.
  `dusk reset-gamma` and `dusk list` show the same error when it is missing.
- **The colors flicker or a warning says another program changes them.** Turn off GNOME's Night
  Light (Settings > Displays > Night Light); dusk replaces it.
- **The screen is too dark.** Software brightness scales the whole picture; raise the `floor` of the
  monitor, raise the schedule values, or use `brightness = "ddc"` for that monitor.
- **`brightness = "ddc"` does nothing.** The monitor needs DDC/CI enabled in its menu and access
  to `/dev/i2c-*` (add yourself to the `i2c` group and log in again, or `sudo modprobe i2c-dev`).
  `dusk list` should show a DDC bus for it.
- **See what it is doing.** `dusk status` and `dusk logs -f` (or the Service tab).
- **`dusk status` says dusk is not running.** Start it with `dusk start`. If that fails, the service
  may not be installed yet (`dusk install`); `dusk logs` shows why it did not come up.

## Uninstall

```sh
just uninstall      # or `dusk uninstall`
```

The service is stopped and removed; the configuration is kept.

## Development

```sh
just check          # gofmt, go vet and go test -race: the same command CI runs
just english-check  # the repository is written in English (no non-ASCII outside author headers)
```

The tests never touch a monitor, the D-Bus session or a real terminal: the gamma backend, DDC, the
service manager and the daemon are all behind small interfaces with fakes.
