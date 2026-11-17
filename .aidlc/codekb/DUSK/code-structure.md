# Code structure — DUSK
Status: draft · Confidence: high

| Caminho | Responsabilidade |
|---|---|
| cmd/dusk | roteamento de subcomandos, `status`, `reset-gamma`, `reset-ddc`, TUI, adaptadores `cliService`/`cliReset` |
| internal/gamma | `Ramp`, `Identity`, `Build`, `BrightnessScale`, `Backend`, `Reset`, `Lazy`; `gammatest` (fake); `mutter` (godbus) |
| internal/controller | laço de Tick: cor e brilho por software, brilho DDC opcional, reaplicação, pausa, shutdown |
| internal/config | TOML → `Config` (brightness, floor, warnings), `Marshal`, `Validate`, `Write` (backup + atômico) |
| internal/schedule | funções puras: Target, NightIntensity, NightPhase, NightGains, Step |
| internal/monitors | `Resolve` (saída Mutter ↔ config ↔ Display DDC), `Discover` (ddcutil) |
| internal/ddc (+ddctest) | adaptador ddcutil e fakes |
| internal/legacy | `Plan`/`Apply`/`Forget` do reset DDC |
| internal/service, install, logs | systemctl user, unit (`ExecStopPost`), journalctl |
| internal/ipc, daemon | socket unix JSON + handler |
| internal/tui | modelo Bubble Tea: abas, formulários, Status, Monitors, Night, Service |
| internal/state | leitura legada de `state.json` |
| internal/prompt | confirmações do `install`/`uninstall` |

Onde adicionar: novo backend de cor = implementar `gamma.Backend`; nova aba = campo no `Model` + `tabKey` + `View`. [src: paths]
