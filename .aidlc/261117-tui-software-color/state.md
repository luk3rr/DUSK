# AI-DLC State — tui-software-color

- Intent: 261117-tui-software-color
- Status: IN_PROGRESS
- Profile: custom (classic + 1.1)   Depth: Standard   Tests: Standard
- Project type: brownfield   Repos: DUSK
- Switches: collaborators=off reviewers=off plan_approval=off walking_skeleton=off autonomy=automatic-checkpoints iteration=unit-major
- Verification command: `test -z "$(gofmt -l .)" && go vet ./... && go test -race ./... && just english-check` (approved 2026-11-17)
- Language: chat=pt-BR files=pt-BR
- Current: phase=construction stage=3.1 Functional Design   Updated: 2026-11-17

## Route
Legend: [x] done · [>] current · [ ] pending · [S] skipped (reason) · [!] needs redo
### Inception
- [x] 1.1 Intent Capture / Feasibility (spike OK, efeito visual confirmado)
- [x] 2.1 Reverse Engineering (codekb)
- [x] 2.2 Team Practices Discovery
- [x] 2.3 Requirements Analysis
- [x] 2.4 User Stories
- [x] 2.5 Refined Mockups (TUI)
- [x] 2.6 Domain & Components
- [x] 2.7 Units of Work
- [x] 2.8 Contracts
- [x] 2.9 Delivery Planning
### Construction
- [x] 3.1 Functional Design
- [x] 3.2 NFR Requirements
- [x] 3.3 NFR Design
- [S] 3.4 Infrastructure Design (sem infraestrutura; a unit systemd é coberta por teste na UNIT-4)
- [x] 3.5 Code Generation
- [x] 3.6 Build & Test

## Environment
- Previous intent: 261113-display-brightness-scheduler (COMPLETE). Ubuntu, GNOME Wayland, NVIDIA RTX 4060. org.gnome.Mutter.DisplayConfig exposes GetCrtcGamma/SetCrtcGamma (introspected, not yet exercised).

## Units (filled by 2.7)

| Unit | Depends on | Status | Verified |
|---|---|---|---|
| UNIT-1 Gamma + Mutter (skeleton) | - | verified | yes (2026-11-17, exit 0) |
| UNIT-2 Config e identidade | UNIT-1 | verified | yes (2026-11-17, exit 0) |
| UNIT-3 Controller por software | UNIT-1, UNIT-2 | verified | yes (2026-11-17, exit 0) |
| UNIT-4 Legado, reset-ddc, serviço | UNIT-3 | verified | yes (2026-11-17, exit 0) |
| UNIT-5 TUI núcleo e Status | UNIT-3 | verified | yes (2026-11-17, exit 0) |
| UNIT-6 TUI Monitors e Night | UNIT-2, UNIT-5 | verified | yes (2026-11-17, exit 0) |
| UNIT-7 TUI Service e Logs | UNIT-4, UNIT-5 | verified | yes (2026-11-17, exit 0) |
| UNIT-8 CI e docs | UNIT-6, UNIT-7 | verified | yes (2026-11-17, exit 0) |

## Open assumptions
(none yet)

## Memory candidates
(M-004 and M-005 approved 2026-11-17 and written to memory/project.md)
