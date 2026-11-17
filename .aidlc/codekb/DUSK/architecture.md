# Architecture — DUSK
Status: draft · Confidence: high

Binário Go único (daemon + CLI + TUI). Núcleo puro (`config`, `schedule`, `gamma.Build`) e portas injetáveis (`gamma.Backend`, `ddc.Display`, `tui.Client/ConfigStore/Service/DDCReset`). [src: internal/controller/controller.go:37, internal/tui]

```mermaid
flowchart TB
  CLI[cmd/dusk] --> TUI[internal/tui Bubble Tea]
  CLI -->|ipc socket| DMN[daemon]
  TUI -->|status, reload, pause| DMN
  TUI -->|grava atômico + .bak| CFG[(config.toml)]
  TUI --> SVC[service/install/logs]
  TUI --> LEG[legacy reset-ddc]
  DMN --> CTL[controller]
  CTL --> SCH[schedule + gamma.Build]
  CTL --> GB[gamma.Backend]
  GB --> MUT[mutter: godbus]
  CTL -.opcional.-> DDC[ddc.Display 0x10]
  LEG -.escreve 0x16/18/1A só com confirmação.-> DDC
```
Caption: `ddc` só entra se algum monitor usa `brightness = "ddc"`; o caminho normal não chama `ddcutil`.

- Concorrência: um goroutine por monitor por tick; `Display` serializa por mutex. [src: controller.go Tick]
- Reaplicação: verificação de 1 min + evento `MonitorsChanged` + Resume; aviso após 3 divergências em 10 min. [src: controller.go]
- Decisões: ADR-10..16 em `.aidlc/261117-tui-software-color/design.md`.
