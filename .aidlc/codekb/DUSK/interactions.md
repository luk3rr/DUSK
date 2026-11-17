# Interactions — DUSK
Status: draft · Confidence: medium

```mermaid
sequenceDiagram
  participant T as Tick
  participant S as schedule
  participant G as gamma.Backend
  participant D as ddc (opcional)
  T->>S: NightGains, Target (relógio injetado)
  T->>G: Get a cada 1 min (rampa mudou?)
  T->>G: Set em passos de 1 ponto, 40 ms
  T->>D: setvcp 0x10 só se brightness = ddc
```
Caption: laço principal; zero chamadas DDC com backend software. Shutdown/pausa: identidade nas rampas; `ExecStopPost=reset-gamma` cobre crash. [src: controller.go, install.go]
