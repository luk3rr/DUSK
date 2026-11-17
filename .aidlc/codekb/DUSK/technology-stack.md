# Technology stack — DUSK
Status: draft · Confidence: high

- Go 1.26.8. Dependências diretas: BurntSushi/toml, godbus/dbus/v5, charmbracelet/bubbletea v1.3.10, lipgloss v1.1.0, charmbracelet/x/term. [src: go.mod]
- Em execução: GNOME/Mutter (D-Bus), systemd user, journalctl; opcional `ddcutil` + `/dev/i2c-*`.
- Build: `just`; CI: GitHub Actions (`.github/workflows/ci.yml` roda `just check` e `just english-check`). [src: justfile, .github]
