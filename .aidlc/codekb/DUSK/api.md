# API — DUSK
Status: draft · Confidence: high

**IPC** (socket unix, JSON por linha, 0600): `status`, `pause`, `resume`, `reload`, `stop`; sem valores na requisição. `status` devolve por monitor: output, bus/DDC, backend de brilho, piso, atual/alvo, gama aplicada, escala, nível noturno, agenda, erros; mais `warnings`. [src: internal/ipc/ipc.go]

**CLI**: sem argumentos (TTY) abre a TUI; `setup` = TUI; `run`, `install`, `uninstall`, `start`, `stop`, `status`, `pause`, `resume`, `reload`, `logs`, `list`, `check-config`, `reset-gamma`, `reset-ddc [--yes] [--to-max]`. [src: cmd/dusk/main.go]

**Consumidas**: `org.gnome.Mutter.DisplayConfig` (GetResources, Get/SetCrtcGamma, sinal MonitorsChanged); opcional `ddcutil detect|getvcp|setvcp`; `systemctl --user`; `journalctl --user`.
