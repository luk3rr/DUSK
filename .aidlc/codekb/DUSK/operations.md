# Operations — DUSK
Status: draft · Confidence: high

| Ação | Comando |
|---|---|
| build | `just build` |
| verificação | `test -z "$(gofmt -l .)" && go vet ./... && go test -race ./... && just english-check` (`just check` + `just english-check`) |
| instalar | `just install` ou tecla `i` na aba Service (precisa de config) |
| CI | GitHub Actions: `just check` e `just english-check` |

Observabilidade: slog → journalctl (`dusk logs`, aba Service). Sem releases.
