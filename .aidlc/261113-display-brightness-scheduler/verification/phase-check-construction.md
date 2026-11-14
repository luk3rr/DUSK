# Phase check — Construction (2026-11-13)

## 1. Inventário de artefatos

| Artefato | Estado |
|---|---|
| Código e testes no repositório (cmd/dusk, internal/**) | ✓ |
| design.md § 3.x por Unit (UNIT-1 a UNIT-6) | ✓ |
| tasks.md com todas as tarefas marcadas e resumos por Unit | ✓ |
| construction/build-and-test.md (3.6) | ✓ |
| 3.4 Infrastructure Design | [S] sem recursos de nuvem; a unit systemd está desenhada na UNIT-6 |
| 3.7 CI | fora da rota do perfil `classic` |
| README.md (inglês) e justfile | ✓ |

## 2. Rastreabilidade Unit → código → testes → resultado

| Unit | Código | Testes | Resultado |
|---|---|---|---|
| UNIT-1 core | internal/config, internal/schedule | config_test, marshal_test, schedule_test | verified |
| UNIT-2 monitores | internal/ddc, internal/ddc/ddctest, internal/monitors | ddc_test, monitors_test | verified; `dusk list` real |
| UNIT-3 controller | internal/controller, internal/nightlight, internal/state | controller_test, nightlight_test, state_test | verified; 6 mutações |
| UNIT-0 rename, inglês, backend auto | repositório inteiro | testes existentes + `just english-check` | verified |
| UNIT-4 IPC e CLI | internal/ipc, internal/daemon, cmd/dusk | ipc_test, daemon_test, main_test | verified; 3 mutações |
| UNIT-5 setup | internal/prompt, internal/preflight, internal/setup | prompt_test, preflight_test, setup_test | verified; 4 mutações; setup real somente leitura |
| UNIT-6 instalação | internal/install, cmd/dusk | install_test, main_test | verified; 3 mutações; systemd-analyze |

Cada NFR tem desenho (design.md) e validação (matriz em construction/build-and-test.md). Lacunas declaradas: NFR-3 sem medição de CPU/memória; nenhum teste ponta a ponta com hardware, Night Light e systemd reais feito pelo agente.

## 3. Consistência
- IDs e termos: `dusk` em todo o repositório; `night.fallback` é `auto`/`off` no código, nos testes, no README e em design.md.
- Durante este gate encontrei e corrigi três trechos de design.md que ainda diziam `night.fallback = "gnome"` (§ 2.8 e § 3.x da UNIT-3) e uma frase mutilada na emenda de requirements.md; agora todos dizem `auto`/`off`, como o código.
- A numeração de UNIT-0 após UNIT-3 foi intencional (refactor transversal); o DAG em design.md § 2.7 a mostra.

## 4. Veredito: PASS

Riscos aceitos: ver "Falhas conhecidas e riscos residuais" em construction/build-and-test.md (hardware real, A-17, A-8, NFR-3, scanner de vulnerabilidades ausente).
