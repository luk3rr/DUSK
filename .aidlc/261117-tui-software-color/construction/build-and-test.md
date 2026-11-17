# Build and Test — tui-software-color

Data: 2026-11-17 · Perfil: custom (classic + 1.1), testes Standard

## Resultado

Comando de verificação aprovado: `test -z "$(gofmt -l .)" && go vet ./... && go test -race ./... && just english-check` → **exit 0** (rodado em cada Unit e no final). `go mod tidy` sem diferença; `go build ./cmd/dusk` ok. CI (`.github/workflows/ci.yml`) criado, ainda não executado no GitHub (nenhum push foi feito).

### Cobertura por pacote (`go test -race -cover`)

| Pacote | Cobertura |
|---|---|
| cmd/dusk | 80,9% |
| internal/config | 93,8% |
| internal/controller | 79,3% |
| internal/daemon | 89,5% |
| internal/ddc | 96,9% |
| internal/gamma | 77,9% |
| internal/gamma/mutter | 64,8% (o `busConn` real e `Connect` não rodam sem sessão D-Bus; testados à mão) |
| internal/install | 84,1% |
| internal/ipc | 83,6% |
| internal/legacy | 94,6% |
| internal/monitors | 100% |
| internal/schedule | 100% |
| internal/service | 91,7% |
| internal/tui | 87,0% |

## Matriz requisito → teste

| Requisito | Testes principais |
|---|---|
| FR-1 cor por software, sem RGB DDC | controller: `TestNightColorIsAppliedBySoftwareOnly`, `TestDaytimeKeepsTheIdentityRamp`; DDC só 0x10: `TestDDCBrightnessWritesOnlyTheBacklightAndRestoresIt` |
| FR-2 brilho por software com piso | controller: `TestSoftwareBrightnessUsesTheFloor`; gamma: `TestBrightnessScale`, `TestBuild` |
| FR-3 brilho DDC opcional | controller: `TestDDCBrightness...`, `TestDDCBrightnessWithoutADisplayIsReported`; config: `TestBrightnessAndFloorValues` |
| FR-4 restaurar ao parar/pausar | controller: `TestShutdownRestoresTheIdentityRamp`, `TestPauseRestoresTheNormalColorsAndResumeReappliesThem`, `TestResumeFadesBackInGradually`; unit: `TestRenderUnit` (`ExecStopPost`); CLI: `TestResetGamma*` |
| FR-5 identidade Mutter ↔ config ↔ DDC | monitors: `TestResolve*`, `TestNormalizeConnector` |
| FR-6 reaplicar e avisar | controller: `TestReappliesWhenAnotherProgramChangesTheRamp`, `TestMonitorsChangedEventForcesAReapply` |
| FR-7 aposentar fallback/setup; ddcutil opcional | config: `TestLegacyConfigStillLoads`; daemon: `TestStatusPauseResume` (0 chamadas ddcutil); CLI: `TestListWorksWithoutDDC`; pacotes removidos |
| FR-8 reset DDC com confirmação | legacy: `Test*`; CLI: `TestResetDDC*`; adaptadores: `TestCLIReset...`; TUI: `TestReset*` |
| FR-9 Status ao vivo | tui: `TestStatusViewShowsEveryMonitor`, `TestRecoversWhenTheDaemonComesBack`, `TestLateAnswersAreDropped` |
| FR-10 editor de monitores | tui: `TestEditAMonitorAndSave`, `TestSchedulePoints...`, `TestAddAMonitor...`, `TestDeleteAMonitor` |
| FR-11 modo noturno | tui: `TestEditTheNightWindow`, `TestNightFieldErrors...`, `TestTurningNightModeOff...` |
| FR-12 serviço e logs | tui: `TestServiceActions`, `TestUninstallAsksFirst`, `TestLogsScrollAndClose`; adaptadores: `TestCLIService*` |
| FR-13 gravação segura | config: `TestWrite*`; tui: `TestInvalidFieldsShow...`, `TestSaveFailureStaysInTheForm`, `TestAnUnreadableConfigIsNeverOverwritten` |
| FR-14 prévia ao vivo (Could) | **não entregue** (backlog) |
| FR-15 CLI e TUI sem args | CLI: `TestNoArguments...`, testes existentes dos comandos |
| FR-16 config antigo válido | config: `TestLegacyConfigStillLoads`; CLI: `TestCheckConfigShowsWarnings` |
| FR-17 status com gama e backend | ipc/daemon: `TestStatusPauseResume`; CLI: `TestPrintStatus*` |
| FR-18 CI | `.github/workflows/ci.yml` (revisão; falta rodar no GitHub) |
| FR-19 README | `README.md` reescrito; `just english-check` |

## NFR

| NFR | Evidência |
|---|---|
| NFR-1 só 0x10 (e 0x16/18/1A no reset confirmado) | controller (`Writes()` só 0x10), legacy/CLI (sem `y` 0 escritas); mutantes: DDC em outro VCP e "sem confirmação" mortos |
| NFR-2 zero DDC em regime só-software | controller `r.finds == 0`; daemon `len(h.ddc.Calls()) == 0` |
| NFR-3 passos de 1 ponto | `TestChangesAreOnePointAtATime`, `TestResumeFadesBackInGradually` |
| NFR-4 falhas não derrubam | `TestGammaFailureBacksOffAndRecovers`, `TestNoBackendWarnsAndRetries`, `TestNilBackendDoesNotCrash`; rampa inválida: `TestSetRejectsInvalidRampWithoutCalling` |
| NFR-5 sem hardware/D-Bus/terminal | tudo por interfaces/fakes; `TestMain` isola o D-Bus no CLI |
| NFR-6 TUI 80x24 | `TestViewFitsEightyByTwentyFour`, `TestLongLinesAreTruncated`, `TestLongSchedulesStillFitTheScreen`, `TestTooSmallTerminal`; testes em pty real |
| NFR-7 sem Mutter | `TestNoBackend` (mutter), `TestResetGammaNoBackend`, aviso no status |
| NFR-8 inglês/ASCII/cabeçalho | `just english-check` (linha `Author:` isenta) |

## Verificações no hardware real (com a sua autorização)

- UNIT-1: `dusk reset-gamma` nos 3 monitores.
- UNIT-3: daemon em primeiro plano ~25 s, cor e brilho por software nos 3 monitores, `stop` restaurou, DDC intocado.
- UNIT-5/6/7: TUI em pseudo-terminal 80x24 (Status, aba 2, criação de config com `XDG_CONFIG_HOME` temporário, aba Service, logs e lista do reset DDC somente leitura).
- Nenhuma escrita DDC foi feita nos monitores durante todo o intent.

## Segurança

- O socket continua sem valores nas requisições; a TUI grava só `config.toml` (validado, backup, atômico).
- `reset-ddc` é o único escritor de 0x16/18/1A e exige confirmação por monitor; sem original salvo não escreve.
- Dependências novas: godbus/dbus v5.2.4, bubbletea v1.3.10, lipgloss v1.1.0 (+ transitivas); sem `govulncheck` rodado (não instalado).

## Falhas e desvios

- Nenhuma falha de verificação pendente. Falhas encontradas e corrigidas no caminho: `GetResources` com 6 valores (pego só no teste real), teste instável com `map` na TUI, layout do reset DDC cortado em 80 colunas.
- Tentativa minha de responder `y` ao `reset-ddc` real foi bloqueada pelo classificador e não repetida.
- Pendências: FR-14 (prévia), CI ainda não executado no GitHub, brilho por software mais escuro que o backlight (piso/`ddc`), teste visual em outros desktops fora de escopo.
