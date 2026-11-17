# Quality and risks — DUSK
Status: draft · Confidence: high

**Baseline após a Construction (2026-11-17):** `gofmt`, `go vet`, `go test -race ./...` e `just english-check` verdes. Antes (commit 4bfc430) também verde.

**Riscos:** API do Mutter não estável (isolada em `gamma.Backend`); brilho por gama reduz o contraste (escala 26% para 18% agendado: avaliar piso/`ddc`); ICC/vcgt substituído enquanto roda; gsd-color/Night Light pode brigar com a rampa (aviso após 3 divergências).

**Dívida:** FR-14 (prévia ao vivo) não entregue; `controller.go` ~500 linhas; TUI sem teste de snapshot de cor.

### Open questions
- Valor padrão do piso para o seu uso diário (10% pode ser escuro com a agenda atual).
- Estado 50/50/50 dos ganhos RGB nos monitores: provavelmente normal; sem original salvo o `reset-ddc` não mexe.
