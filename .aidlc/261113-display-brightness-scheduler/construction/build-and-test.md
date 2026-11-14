# Build and Test (3.6) — dusk

## Comandos executados

| Comando | Resultado |
|---|---|
| `just check` (`gofmt -l`, `go vet ./...`, `go test -race ./...`) | exit 0 |
| `go clean -testcache && go test -count=1 -race ./...` | 14 pacotes com testes, todos `ok` |
| `just english-check` (sem caracteres não ASCII em `.go`, `justfile`, `*.md`, `*.toml` fora do `.aidlc`) | exit 0 |
| `go test -count=3 -race` nos pacotes concorrentes (controller, ipc, daemon, cmd) | estável, sem flakes |
| `systemd-analyze --user verify` na unit gerada por `install.RenderUnit` | sem erros sobre `dusk.service` |
| `dusk list` real (rodado pelo usuário e por mim, somente leitura) | os 3 monitores detectados, brilho 30/40/25, RGB em todos |
| `dusk setup --yes --config <scratchpad>` real, somente leitura | TOML válido gravado só no scratchpad; `~/.config/dusk` não foi tocado |

Resultado: **150 funções de teste de topo, 0 falhas** (mais 35 subtestes); cobertura total de instrução **85,4%**.

## Cobertura por pacote

| Pacote | Cobertura |
|---|---|
| schedule, monitors | 100,0% |
| config | 98,9% |
| preflight | 98,0% |
| ddc | 96,9% |
| prompt | 92,6% |
| nightlight | 88,9% |
| controller | 88,5% |
| setup | 87,8% |
| daemon | 87,0% |
| install | 84,1% |
| ipc | 83,6% |
| cmd/dusk | 74,5% |
| state | 64,3% (caminhos de erro de I/O) |

## Teste de mutação manual

Em cópias do repositório, mutações de comportamento foram introduzidas e os testes tinham de falhar. Todas foram pegas (16 mutações):

| Área | Mutações pegas |
|---|---|
| controller (6) | não salvar o RGB original antes de escrever; sem espera entre escritas; passo de 2 pontos; fallback sem reduzir DDC; ignorar backoff; shutdown sem restaurar |
| pausa, reload, socket (3) | tick ignora a pausa; reload sem `SetConfig`; socket com modo 0644 |
| setup (4) | sem backup; confirmação ignorada; janela noturna sem validação; entradas de monitores desconectados descartadas |
| install (3) | instalar sem confirmar; `systemctl` sem `--user`; `uninstall` apagando a config |

## Matriz requisito → teste

| Requisito | Testes principais | Lacuna |
|---|---|---|
| FR-1 autostart | `TestInstallHappyPath`, `TestRenderUnit`, `TestInstallAndUninstallCommands`, `systemd-analyze verify` | instalação e inicialização reais não foram executadas (A-29) |
| FR-2 identificar monitores | `TestParseDetectFixture`, `TestDiscover`, `TestListReadOnly`, `dusk list` real | nenhuma |
| FR-3 redescoberta | `TestRescanPicksUpNewMonitor`, `TestOfflineMonitorTriggersRescanWithinAMinute`, `TestDiscoverFailureKeepsCurrentMonitors` | hotplug físico não testado |
| FR-4 identidade modelo+serial | `TestMatch`, `TestMatchSurvivesPortChange` | nenhuma |
| FR-5 interpolação | `TestTarget`, `TestTargetMonotonicBetweenPoints`, `TestTargetDSTNoLargeJump` | nenhuma |
| FR-6 passos pequenos | `TestStep`, `TestStepRampWritesAtMostTwentyTimes`, `TestBrightnessConvergesInStepsOfOne`, `TestNoWritesWhenAtTarget` | escrita real em monitor não exercida |
| FR-7 janela e rampa noturnas | `TestNightIntensity`, `TestNightIntensityWindowSameDay`, `TestParseValid` | nenhuma |
| FR-8 reduzir azul por DDC | `TestNightGains`, `TestNightRGBSavesOriginalBeforeFirstWrite` | RGB real em monitor não exercido |
| FR-9, FR-17 fallback do Night Light | `TestFallbackForMonitorWithoutRGB`, `TestFallbackOffNeverTouchesNightLight`, `TestNoFallbackWhenAllMonitorsHaveRGB`, `TestFallbackWhenRGBWriteFails`, `TestAutoFallbackWithoutBackendWarnsOnceAndUsesDDCOnly`, `TestDetect`, testes de `nightlight` | comportamento real do gsd-night-light com `schedule 0.0 a 24.0` (A-17) não verificado |
| FR-10 restaurar RGB | `TestNightRestoresAtDayAndClearsOriginal`, `TestRestartMidNightKeepsOriginal`, `TestShutdownRestoresRGBAtOnceAndNightLight`, `TestRunStopsOnCancelAndShutsDown` | nenhuma além do hardware real |
| FR-11 agenda sempre vence | `TestManualChangeIsReapplied` | nenhuma |
| FR-12 pause/stop/status | `TestPauseFreezesWritesAndResumeContinues`, `TestPauseInterruptsAConvergenceInProgress`, `TestStatusPauseResume`, `TestStopEndsTheDaemonAndRemovesTheSocket`, `TestControlCommandsAgainstARunningDaemon` | nenhuma |
| FR-13 reload e validação | `TestParseInvalid` (18 casos), `TestReloadKeepsOldConfigWhenInvalid`, `TestReloadAppliesNewConfig` | nenhuma |
| FR-14 install/uninstall | `TestInstall*`, `TestUninstall*`, `TestDecliningChangesNothing`, `TestOnlyUserScopeIsEverUsed` | systemd real |
| FR-15 setup interativo | `TestAcceptingEveryDefault`, `TestCustomAnswers`, `TestExistingConfigBecomesDefaultsAndIsBackedUp`, `TestDecliningLeavesTheFileUntouched`, `TestYesModeNeedsNoInput`, `TestSetupYesWritesTheDefaultConfig`, testes de `prompt` e `Marshal`, execução real somente leitura | nenhuma |
| FR-16 nada específico da máquina | varredura estática sem ocorrências no código de produção; `TestNoMonitors`, `TestPreflightFailureStopsTheWizard`, `TestSameModelWithoutSerialSharesOneEntry`, testes de `preflight` | só validado nesta máquina |
| NFR-1 percepção | `TestBrightnessConvergesInStepsOfOne`, `TestNightRGBSavesOriginalBeforeFirstWrite`, `TestStepRampWritesAtMostTwentyTimes` | nenhuma |
| NFR-2 segurança do hardware | `TestSetWhitelistAndClamp`, `TestSetUnsupportedAndUnprobed`, `assertReadOnly` do setup, `FakeRunner` sem `setvcp` em `Discover`/`list`/`setup` | nenhuma |
| NFR-3 recursos | sem escrita quando no alvo (`TestNoWritesWhenAtTarget`) | **CPU e memória não foram medidos**; benchmark não criado |
| NFR-4 resiliência | `TestFailureIsolationAndBackoff`, `TestBackoffGrowsUpToFiveMinutes`, `TestSecondInstanceIsRefused`, `TestListenModeAndDuplicateAndStale` | nenhuma |
| NFR-5 observabilidade | `TestLogsRelevantEvents`, `TestFailureIsolationAndBackoff` | journal real não inspecionado |
| NFR-6 / NFR-9 portabilidade | `TestRenderUnit`, `systemd-analyze verify`, testes de `preflight` | só testado em Ubuntu/GNOME/Wayland |
| NFR-7 dependências | `go.mod` com uma dependência (`BurntSushi/toml`) | nenhuma |
| NFR-8 precisão | testes de tabela de `schedule` | nenhuma |
| NFR-10 idioma | `just english-check` | nenhuma |

## Notas de segurança

- Escritas em monitores só nos VCP 0x10, 0x16, 0x18 e 0x1A, limitadas a `[0, máx]` (M-002). Testes nunca tocam hardware, `gsettings` ou `systemctl` reais (M-001).
- Socket de controle em `$XDG_RUNTIME_DIR` com modo 0600; comandos sem argumentos vindos do cliente; recusa de segundo daemon; recuperação de socket órfão.
- Estado em `state.json` com modo 0600, gravação atômica; o RGB original é salvo em disco antes da primeira escrita.
- `setup`, `install` e `uninstall` pedem confirmação antes de mexer na máquina; `setup` faz backup do arquivo existente.
- Dependência única `github.com/BurntSushi/toml v1.6.0`. **Nenhum scanner de vulnerabilidades** (`govulncheck`) estava instalado; a varredura não foi feita.
- Um incidente de processo durante o trabalho: um heredoc sem aspas fez o shell tentar executar trechos entre crases de um texto. Verifiquei depois que nada foi executado (sem unit systemd, sem binário instalado, sem config do usuário, `justfile` intacto).

## Falhas conhecidas e riscos residuais

1. Nenhuma escrita real foi feita em monitores, no Night Light ou no systemd pelo agente. O comportamento ponta a ponta com hardware real depende de um teste supervisionado do usuário (A-23, A-29).
2. A-17: a combinação `schedule-automatic=false`, `from=0.0`, `to=24.0` do GNOME não foi verificada. O fallback não deve ligar neste PC (os 3 monitores aceitam RGB).
3. A-8: reduzir pela metade a intensidade DDC quando o fallback está ligado é uma estimativa.
4. NFR-3 sem medição de CPU e memória.
5. Dois monitores do mesmo modelo e sem serial compartilham uma entrada (A-25).
6. Cobertura de `state` (64,3%) e `cmd/dusk` (74,5%) menor; os caminhos não cobertos são erros de I/O e de sistema.
