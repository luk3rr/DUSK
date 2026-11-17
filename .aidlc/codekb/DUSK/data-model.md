# Data model — DUSK
Status: draft · Confidence: high

Sem banco. Arquivos: `~/.config/dusk/config.toml` (usuário/TUI; backup `.bak-<ts>` a cada gravação), `~/.local/state/dusk/state.json` (só leitura; originais RGB de versões antigas), `$XDG_RUNTIME_DIR/dusk.sock` (socket), `~/.config/systemd/user/dusk.service`.

```mermaid
erDiagram
  CONFIG ||--o{ MONITOR : has
  MONITOR ||--|{ POINT : schedule
  MONITOR ||--o| MONITOR_NIGHT : override
  CONFIG ||--|| NIGHT : global
```
Caption: entidades do `config.toml`; `Monitor` ganhou `brightness` e `floor`.
