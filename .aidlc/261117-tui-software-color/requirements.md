# Requirements — tui-software-color

## Original request

Controlar o modo noturno via driver direto no monitor é ruim, pois ao desligar o computador no período noturno e, por exemplo, usar o monitor no outro dia em outro computador mantém as configurações do momento em que o computador foi desligado na noite anterior de forma persistente. Diante disso, e também da dificuldade em interagir com a app via somente linha de comando, proponho: 1) interação completa via TUI; 2) controle das cores RGB via software em vez de hardware e, se possível, das configurações de brilho também (essa não tenho certeza se é possível, então, caso não seja, pode continuar via hardware)

## 1.1 Intent & stakeholders

**Problema (negócio).** O modo noturno atual grava ganho RGB/brilho na memória do monitor via DDC/CI (VCP 0x10/0x16/0x18/0x1A). Ao desligar o PC à noite, o monitor mantém o estado noturno e o vaza para outro computador no dia seguinte. Além disso, operar o dusk só por linha de comando é difícil. [src: Original request]

**Intenção.** (1) interação completa via TUI; (2) cores RGB por software em vez de hardware; (3) brilho por software, com DDC como opção por monitor. [src: Q1,Q2]

**Medidas de sucesso.**
- S1: nada é gravado no monitor por padrão; após parar/desligar o dusk, `ddcutil getvcp 0x10/0x16/0x18/0x1A` mostra os valores originais. [src: Q4]
- S2: configurar monitores, agendas, modo noturno, pausar/retomar e ver status é possível sem editar TOML nem usar subcomandos. [src: Q4]
- S3: CLI e daemon atuais seguem funcionando (compatibilidade). [src: Q4]

**Não-objetivos.** Outros desktops (KDE, wlroots, X11) neste intent; só GNOME Wayland. [src: Q4]

**Stakeholders.**

| Papel | Quem |
|---|---|
| Decisor / usuário | Lucas Araújo (autor e único usuário conhecido) |
| Afetado | Outros usuários do repositório público (M-003) |

### Assumptions & open questions
- A1: o brilho por software (gama) é aceito como substituto visual do backlight; o DDC permanece opcional por monitor. [src: Q2]
## 1.3 Feasibility, constraints & RAID

**Spike (2026-11-17, GNOME 49-like Wayland, NVIDIA RTX 4060, 3 saídas).** `org.gnome.Mutter.DisplayConfig` expõe `GetCrtcGamma`/`SetCrtcGamma` por CRTC (rampa de 1024 pontos, uint16). No CRTC 0 (DP-1): leitura OK; escrita com azul a 80% aceita; leitura de volta confere (52428); restauração da rampa original idêntica. Com night light do GNOME desligado, sem conflito observado. Efeito visual confirmado pelo usuário no gate. [src: spike]

**Recomendação:** GO. Rampa por CRTC permite RGB e brilho por software, por monitor, sem persistir no hardware.

| ID | Tipo | Item | Mitigação |
|---|---|---|---|
| R1 | Risco | gsd-color/Mutter pode sobrescrever a rampa (hotplug, resume, night light ligado) | reaplicar em eventos/periodicamente; detectar night light ligado e avisar |
| R2 | Risco | Rampa some se o dusk morrer; Mutter pode não restaurar | restaurar rampa original em stop/sinais; nada fica no hardware |
| R3 | Risco | Mapeamento conector DDC (`ddcutil`) ↔ CRTC/output Mutter | casar por EDID/serial/nome do conector |
| R4 | Risco | Brilho por gama reduz contraste e não baixa o backlight | DDC opcional por monitor; documentar |
| R5 | Dependência | API D-Bus do Mutter não é estável/pública | isolar atrás de adaptador mockável (M-001) |
| R6 | Dependência | Biblioteca de TUI em Go (nova dependência) | decidir em 2.6 (ADR) |
| I1 | Issue | Valores DDC já gravados hoje nos monitores podem estar em estado noturno | `dusk` deve oferecer reset/restauro DDC |

### Assumptions & open questions
- A2: apenas GNOME Wayland (Mutter) neste intent. [src: Q4]
## 1.4 Scope
## 2.3 Requirements

### Functional

| ID | Requirement | Rationale | Acceptance criteria | Priority | Source |
|---|---|---|---|---|---|
| FR-1 | O modo noturno aplica o balanço de cor (R, G, B) por monitor via rampa de gama do Mutter (`SetCrtcGamma`), com os mesmos níveis e rampa de hoje; o dusk não escreve VCP 0x16/0x18/0x1A. | Estado não persiste no monitor. | Dado modo noturno ativo, quando o tick roda, então a rampa do CRTC do monitor tem o ganho esperado e nenhum `setvcp 16/18/1A` é emitido. | Must | [src: Q1, spike] |
| FR-2 | O brilho agendado é aplicado por padrão por software (escala da rampa de gama), com piso configurável por monitor (padrão 10%). | Pedido do usuário; evita tela preta. | Dado agenda 0%, então o ganho de gama aplicado é o piso; valores 0–100 mapeiam linearmente de piso a 100%. | Must | [src: Q2, gate 2.3] |
| FR-3 | Cada monitor pode optar por brilho via DDC (`brightness = "ddc"`, VCP 0x10), mantendo os passos de 1 ponto atuais. | Backlight real quando desejado. | Dado o monitor com backend DDC, então só `setvcp 10` é escrito, nunca outro VCP. | Should | [src: Q2] |
| FR-4 | Ao parar, pausar ou receber SIGTERM/SIGINT, o dusk restaura a rampa de gama original (identidade lida no início) de todos os monitores. | Nada de estado residual. | Dado `dusk stop`, então `GetCrtcGamma` devolve a rampa lida no start. | Must | [src: Q4 S1] |
| FR-5 | Cada monitor do Mutter é associado ao monitor da configuração pelo nome do conector e/ou EDID (modelo + serial), sem depender de DDC. | Software funciona em monitor sem DDC. | Dado monitor sem DDC, ele é controlado por gama; dado monitor com DDC, conector DRM `card1-DP-1` casa com saída `DP-1`. | Must | [src: R3] |
| FR-6 | O dusk detecta que a rampa foi alterada por terceiros (hotplug, retomada, Night Light do GNOME) e reaplica; avisa se o Night Light do GNOME estiver ligado. | Evita briga com gsd-color. | Dado rampa sobrescrita, no próximo tick a rampa volta ao alvo; aviso no status/log. | Must | [src: R1] |
| FR-7 | Aposentar: fallback Night Light do GNOME, `state.json` de ganhos RGB e o assistente `setup` por prompts; `ddcutil` vira opcional (só para brilho DDC e `reset-ddc`). | Redundantes com gama/TUI. | Dado `ddcutil` ausente, o dusk inicia e controla via gama; `dusk setup` abre a TUI. | Must | [src: codekb, gate 2.1] |
| FR-8 | `dusk reset-ddc` (e ação na TUI) restaura 0x16/0x18/0x1A ao valor original salvo ou a 100%, após confirmação explícita, por monitor. | Limpar estado noturno gravado por versões antigas (I1). | Dado confirmação, os ganhos voltam; sem confirmação nada é escrito. | Must | [src: Q3] |
| FR-9 | A TUI mostra um painel ao vivo: por monitor, brilho atual/alvo, cor atual/alvo (RGB), backend, fase da noite, próxima mudança, pausado/rodando. | Substitui `status` por CLI. | Painel atualiza ≤ a cada 1 s a partir do daemon. | Must | [src: Q4 telas] |
| FR-10 | A TUI edita monitores: nome, backend de brilho, piso, pontos da agenda e nível noturno RGB por monitor. | Interação completa. | Cada campo editável é persistido no `config.toml` e refletido após reload. | Must | [src: Q4 telas] |
| FR-11 | A TUI edita o modo noturno global: ligar/desligar, início, fim, rampa e níveis padrão. | Idem. | Idem. | Must | [src: Q4 telas] |
| FR-12 | A TUI controla o serviço e logs: instalar/desinstalar, iniciar/parar, pausar/retomar, ver logs, reset DDC. | Elimina a CLI como único caminho. | Cada ação executa o equivalente do subcomando e mostra o resultado. | Must | [src: Q4 telas] |
| FR-13 | A TUI grava `config.toml` de forma atômica, com validação e backup, e envia `reload` ao daemon; erros de validação aparecem no campo. | TOML continua fonte da verdade. | Config inválida nunca é gravada; arquivo anterior fica em `.bak-<timestamp>`. | Must | [src: Q1 TUI x daemon] |
| FR-14 | Prévia ao vivo: ao editar cor/brilho, a TUI pode aplicar valores temporários via IPC que expiram sozinhos. | Ver o efeito antes de salvar. | Valor temporário expira em ≤ 30 s ou ao sair; nunca é gravado em disco. | Could | [src: Q1] |
| FR-15 | A CLI atual continua: `run`, `status`, `pause`, `resume`, `start`, `stop`, `reload`, `logs`, `install`, `uninstall`, `check-config`, `list`; `dusk` sem argumentos num terminal abre a TUI. | Compatibilidade. | `just check` dos testes de CLI existentes passa (ajustados ao que foi aposentado). | Must | [src: Q4 S3] |
| FR-16 | O `config.toml` atual continua carregando: chaves novas têm padrão; `fallback` é aceita e ignorada com aviso. | Migração sem quebra. | Config de 2026-11-13 do usuário passa em `check-config` e produz o mesmo alvo de cor. | Must | [assumption A3] |
| FR-17 | O status (IPC/CLI/TUI) reporta ganhos de gama aplicados e backend por monitor. | Observabilidade. | `dusk status` lista R/G/B e brilho por software. | Must | [src: FR anterior 21] |
| FR-18 | CI no GitHub Actions executa `just check` e `just english-check` em push e PR. | Prática afirmada no gate 2.2. | Workflow verde no repositório. | Should | [src: gate 2.2] |
| FR-19 | README documenta software vs DDC, TUI, migração e `reset-ddc`. | Usuários de outros PCs (M-003). | README em inglês cobre os itens. | Should | [src: M-003] |

| FR-20 | O modo noturno aceita uma temperatura de cor em kelvin (`night.temperature`, 1700-6500 K; 6500 = sem mudança) que vence os ganhos RGB; por monitor, `night = { temperature = N }`. | Ajuste simples, como o Night Light do GNOME. | Dado `temperature = 3400`, os ganhos derivam da curva de corpo negro e o config volta idêntico após `Marshal`/`Parse`. | Must | [src: feedback do usuário, emenda 2026-11-17] |
| FR-21 | A TUI usa sliders com barra para todo valor em percentual, a temperatura e a rampa (setas, passo grande com Shift/PgUp/PgDn, Home/End); horas se ajustam em passos de 15 min/1 h; só nome e match são digitados. | Digitar números é ruim. | Nenhum campo numérico aceita texto livre. | Must | [src: feedback] |
| FR-22 | Os editores de Night e de monitor oferecem modo "warmth" (temperatura) ou "custom RGB" (três sliders) com amostra de cor ao vivo; trocar de modo preserva a cor. | RGB direto é preciso mas difícil. | Warmth→custom preenche os sliders com os ganhos da temperatura. | Must | [src: feedback] |

### Non-functional

| ID | Category | Measurable target | Verification | Source |
|---|---|---|---|---|
| NFR-1 | Segurança de hardware | O dusk nunca escreve VCP além de 0x10 (e 0x16/18/1A só em `reset-ddc` confirmado). Parar o dusk deixa 0 escritas pendentes. | testes com fake Display; mutação na whitelist | [src: M-002, S1] |
| NFR-2 | Desempenho/travadas | Monitores só-software: 0 chamadas a `ddcutil` em regime; chamadas D-Bus só quando o alvo muda ou a cada verificação (≥ 1 min). CPU média do daemon < 1% em regime. | teste de contagem de chamadas; medição local | [src: FR-22 anterior] |
| NFR-3 | Suavidade | Mudança de cor/brilho em passos ≤ 1 ponto percentual por tick durante rampas; sem salto visível. | teste da função de passo | [src: schedule.Step] |
| NFR-4 | Robustez | Se o D-Bus do Mutter falhar, o monitor fica `Online=false` com backoff, sem derrubar o daemon; rampa inválida nunca é enviada (valores clamp em [0,65535], monotônica). | testes de falha injetada | [src: R2] |
| NFR-5 | Testabilidade | Todo acesso ao Mutter passa por interface mockável; nenhum teste toca hardware/D-Bus real. | `go test -race ./...` sem sessão gráfica | [src: M-001] |
| NFR-6 | Usabilidade TUI | Funciona em 80x24; resposta a tecla < 100 ms; sai restaurando o terminal, inclusive em panic/Ctrl+C. | testes de modelo + checagem manual | [src: Q4] |
| NFR-7 | Portabilidade | Backend de cor atrás de interface; só Mutter implementado; sem GNOME o dusk informa claramente que não há backend de cor (e usa só DDC brilho se houver). | teste de detecção | [src: Q4 não-objetivo] |
| NFR-8 | Idioma/estilo | Repo em inglês ASCII; arquivos novos com cabeçalho. | `just english-check` | [src: M-003] |

### Constraints · Out of scope · Glossary
- Restrições: só GNOME Wayland (Mutter `SetCrtcGamma`, API não estável); Go; dependências novas só com ADR (TUI).
- Fora de escopo: KDE/wlroots/X11; temperatura em kelvin fixa; perfis ICC; integração com sensor de luz.
- Glossário: **rampa de gama** (LUT por CRTC, 1024 pontos uint16); **piso** (brilho mínimo por software); **backend de brilho** (`software` | `ddc`).

### Assumptions & open questions
- A3: o `config.toml` existente deve continuar válido (migração suave, sem comando de conversão). [user, gate 2.3]
- A4: com backend DDC de brilho, ao parar o dusk o brilho original lido no start é restaurado. [user, gate 2.3]
- A5: `dusk` sem argumentos num terminal abre a TUI; `dusk setup` vira alias da TUI. [user, gate 2.3]
- A6: ganhos de cor por software usam a rampa do CRTC inteira (R, G, B multiplicados pelo ganho × escala de brilho). [inferred]
## 2.4 User stories

**Personas.** *Lucas* (usuário diário, 3 monitores, usa outros PCs com os mesmos monitores). *Visitante* (usa o dusk em outro PC, possivelmente com monitor sem DDC). *Contribuidor* (mantém o repositório).

### US-1 Modo noturno sem marcas no monitor (FR-1, FR-2, FR-4, FR-5, FR-6, FR-17) Persona: Lucas
Como Lucas, quero que cor e brilho noturnos sejam aplicados por software, para desligar o PC à noite e encontrar o monitor normal em outro computador.
- Dado o modo noturno ativo, quando eu paro o dusk ou desligo o PC, então a cor volta ao normal e nenhum ganho RGB foi gravado no monitor.
- Dado que o GNOME Night Light reescreve a rampa, quando o próximo tick roda, então o dusk reaplica e avisa.

### US-2 Brilho real quando eu quiser (FR-3) Persona: Lucas
Como Lucas, quero escolher o backend de brilho por monitor, para usar o backlight real num monitor e gama em outro.
- Dado backend `ddc`, quando a agenda muda, então só o VCP 0x10 é escrito, em passos de 1 ponto; ao parar, o brilho original volta.

### US-3 Limpar o estado antigo do monitor (FR-8) Persona: Lucas
Como Lucas, quero restaurar os ganhos RGB que versões antigas gravaram, para que o monitor volte ao padrão.
- Dado confirmação por monitor, quando executo reset DDC, então 0x16/0x18/0x1A voltam ao valor original ou 100.

### US-4 Ver tudo de relance (FR-9, FR-17) Persona: Lucas
Como Lucas, quero um painel ao vivo, para saber brilho, cor atual/alvo, fase da noite e se está pausado.
- Dado o daemon rodando, quando abro a TUI, então vejo cada monitor atualizando a cada ≤ 1 s.

### US-5 Editar monitores e agendas na TUI (FR-10, FR-13, FR-14) Persona: Lucas
Como Lucas, quero editar nome, backend, piso, pontos da agenda e nível RGB, para nunca mexer no TOML.
- Dado valor inválido, quando tento salvar, então o erro aparece no campo e nada é gravado.
- Dado edição válida, quando salvo, então o TOML é gravado com backup e o daemon recarrega.

### US-6 Ajustar o modo noturno (FR-11, FR-13) Persona: Lucas
Como Lucas, quero configurar início, fim, rampa e níveis padrão na TUI.
- Dado novo horário salvo, quando o daemon recarrega, então a fase da noite no painel reflete o novo horário.

### US-7 Controlar o serviço sem comandos (FR-12) Persona: Lucas
Como Lucas, quero instalar, iniciar, parar, pausar e ver logs pela TUI.
- Dado serviço parado, quando escolho Iniciar, então o painel passa a mostrar o daemon rodando.

### US-8 Nada quebra (FR-15, FR-16) Persona: Lucas
Como Lucas, quero que meus comandos e meu config atuais continuem funcionando.
- Dado o config de 2026-11-13, quando rodo `dusk check-config`, então é válido; `dusk` sem argumentos abre a TUI.

### US-9 Usar em qualquer PC e monitor (FR-5, FR-7, FR-19) Persona: Visitante
Como Visitante, quero que funcione sem `ddcutil` e em monitores sem DDC, para usar em outro computador.
- Dado monitor sem DDC, quando inicio o dusk, então cor e brilho funcionam por gama.

### US-10 Qualidade contínua (FR-18) Persona: Contribuidor
Como Contribuidor, quero CI rodando `just check`, para não quebrar o repositório.
- Dado um push, quando o workflow roda, então `just check` e `just english-check` passam.

Cobertura: FR-1..19 cada um em ≥ 1 história (FR-14 em US-5).

### Assumptions & open questions
- Nenhuma nova.
