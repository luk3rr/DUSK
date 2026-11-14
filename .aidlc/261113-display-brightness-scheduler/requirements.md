# Requirements — display-brightness-scheduler

## Original request
Preciso de um programa que vá rodar sempre que meu pc inicializar. Ele deve:
1. Identificar todos os meus monitores
2. Permitir configurar o bilho que quero em cada em em um determinado horário e ir transitando gradualmente
3. Ativar modo noturno em todos eles em horário que eu configurar e ter uma transição sutil

## 2.3 Requirements

Binário único `dusk` (nome provisório, A-1): modo daemon + subcomandos de controle.

### Functional

| ID | Requirement | Rationale | Acceptance criteria | Priority | Source |
|---|---|---|---|---|---|
| FR-1 | O sistema deve iniciar automaticamente no login gráfico como serviço `systemd --user`, reiniciando em falha. | Rodar sempre que o PC inicializar. | Given o PC reiniciado, When faço login, Then `systemctl --user is-active` retorna `active` sem ação manual. | Must | [src: request 1; user: Autostart] |
| FR-2 | O sistema deve identificar todos os monitores com DDC/CI via `ddcutil` e extrair modelo, serial EDID, porta e capacidades (brilho 0x10, ganho RGB 0x16/18/1A). | Base para configurar por monitor. | Given 3 monitores conectados, When o daemon sobe ou rodo `dusk list`, Then os 3 aparecem com modelo, serial (se houver), bus e capacidades. | Must | [src: request 1; probe 2.2] |
| FR-3 | O sistema deve redetectar monitores periodicamente (padrão 5 min) e após falha de escrita, sem reiniciar. | Hotplug e standby. | Given um monitor religado, When passa o intervalo de redeteção, Then ele volta a ser controlado sem reiniciar o serviço. | Must | [src: user: Hotplug] |
| FR-4 | Cada monitor deve ser identificado no TOML por modelo + serial EDID (só modelo se não houver serial) e ter um apelido. | Estável se trocar de porta. | Given o LG sem serial, When configuro por modelo, Then a agenda é aplicada a ele mesmo em outra porta. | Must | [src: user: ID monitor] |
| FR-5 | O usuário deve poder definir, por monitor, uma lista de pontos-chave `horário → brilho (0–100%)`; entre pontos o brilho é interpolado linearmente de forma contínua e circular (depois do último ponto do dia segue ao primeiro do dia seguinte). | Transição gradual. | Given pontos 18:00→40% e 22:00→20%, When são 20:00, Then o brilho alvo é 30%. | Must | [src: request 2; user: Agenda] |
| FR-6 | O sistema deve aplicar o brilho alvo a cada monitor em passos pequenos, escrevendo só quando o valor inteiro muda. | Transição imperceptível e poucas escritas I2C. | Given rampa de 20%, When a agenda desce 20 pontos em 4 h, Then há no máximo 20 escritas, cada uma de ≤1 ponto. | Must | [src: request 2] |
| FR-7 | O usuário deve poder configurar o início e o fim do modo noturno (horários) e a duração da rampa (padrão 30 min), com intensidade configurável. | Modo noturno com horário próprio. | Given início 21:00 e rampa 30 min, When são 21:15, Then a intensidade aplicada é 50% da final. | Must | [src: request 3; user: Transição] |
| FR-8 | O modo noturno deve reduzir o ganho azul (e, se necessário, o vermelho/verde proporcionalmente) via DDC em cada monitor com suporte a RGB. | Controle por monitor em hardware. | Given AOC e LG com RGB, When o modo noturno está ativo, Then 0x1A (azul) está abaixo do valor original. | Must | [src: request 3; user: Modo noturno] |
| FR-9 | Para monitores sem suporte a ganho RGB (ou falha de escrita), o sistema deve usar o Night Light do GNOME (`gsettings`) como fallback global, com transição nativa. | Cobrir "todos eles". | Given um monitor sem 0x16/18/1A, When o modo noturno começa, Then `night-light-enabled` é true e ele é coberto. | Must | [src: user: combinados] |
| FR-10 | Ao encerrar o modo noturno, o sistema deve restaurar o ganho RGB original (lido antes de alterar, persistido em disco) e desativar o Night Light que ele ativou. | Não deixar o monitor "preso" em amarelo. | Given o fim da rampa, When atingido, Then ganhos RGB voltam ao valor original salvo. | Must | [src: request 3] |
| FR-11 | A agenda sempre vence: a cada ciclo o sistema reaplica o alvo, ignorando mudanças manuais no monitor. | Previsibilidade. | Given brilho alterado no botão do monitor, When vem o próximo passo, Then o valor da agenda é reaplicado. | Must | [src: user: Override] |
| FR-12 | O sistema deve oferecer `dusk stop`/`pause` para suspender a agenda (com `resume`) sem desinstalar o serviço, e `dusk status`. | O usuário pode dispensar o app. | Given o daemon ativo, When rodo `dusk pause`, Then nenhuma escrita ocorre até `resume` ou nova inicialização. | Must | [src: user: "comando de stop"] |
| FR-13 | O sistema deve recarregar o TOML sem reiniciar (ou via `dusk reload`) e rejeitar configuração inválida com mensagem clara mantendo a anterior. | Configurar sem fricção. | Given TOML inválido, When recarrego, Then o daemon segue com a config antiga e loga o erro de linha. | Should | [src: user: Config] |
| FR-14 | O sistema deve oferecer `dusk install` que escreve a unit systemd, habilita-a e gera um TOML de exemplo com os monitores detectados. | Fácil de ativar. | Given binário instalado, When rodo `dusk install`, Then unit e config existem e o serviço está habilitado. | Should | [src: request 1] |
| FR-15 | O sistema deve oferecer `dusk setup`, um assistente interativo no terminal que verifica pré-requisitos, identifica os monitores e conduz o usuário (apelido, pontos de brilho, janela e intensidade do modo noturno, fallback) até gravar o TOML, mostrando uma prévia, validando antes de gravar e guardando backup do arquivo anterior. Com `--yes` aceita os padrões sem perguntar. | Funcionar em qualquer computador sem escrever TOML à mão. | Given um PC com 2 monitores quaisquer e nenhuma config, When rodo `dusk setup` e aceito os padrões, Then é criado um TOML válido (`dusk check-config` sai com 0) com uma entrada por monitor. Given uma config existente, When rodo `dusk setup`, Then os valores atuais são os padrões e o arquivo antigo vira backup. | Must | [src: user: "comando de setup ... iterativamente"] |
| FR-16 | Nenhum valor específico de máquina (modelo, serial, bus, fabricante, porta) pode existir no código de produção; tudo vem da descoberta ou da configuração. O sistema deve lidar com 0, 1 ou N monitores, com ou sem serial, com ou sem RGB. | Funcionar em outras configurações. | Given 0 monitores, When rodo `dusk list` ou `setup`, Then recebo uma mensagem acionável (instalar ddcutil, permissão I2C, DDC/CI no monitor) e exit 1. | Must | [src: user: "hardcoded ... outros computadores"] |
| FR-17 | O fallback de modo noturno deve escolher o backend em tempo de execução (`night.fallback = "auto" \| "off"`); hoje o único backend é o Night Light do GNOME, e sem backend disponível o fallback fica desligado com um aviso. | Não presumir GNOME. | Given um PC sem `gsettings`, When o modo noturno começa, Then o programa segue só com DDC e registra o aviso. | Must | [src: user; ADR-8] |

### Non-functional

| ID | Category | Measurable target | Verification | Source |
|---|---|---|---|---|
| NFR-1 | Percepção | Passos de brilho ≤1 ponto e ganho RGB ≤1 ponto por escrita; intervalo mínimo entre escritas por monitor ≥2 s. | Teste unitário do planejador + log de escritas. | [src: request 2/3] |
| NFR-2 | Segurança do hardware | Escritas só em VCP 0x10, 0x16, 0x18, 0x1A (e 0x0C não); valores sempre limitados a [min,max] reportados; nunca escreve em teste. | Testes com adaptador mockado; asserção na whitelist. | [src: A-2] |
| NFR-3 | Recursos | CPU média <1% e memória <30 MB em regime; nenhuma escrita enquanto o alvo não muda. | Medição manual 10 min + contador de escritas. | [inferred] |
| NFR-4 | Resiliência | Falha de um monitor não afeta os outros; falha de escrita gera retry com backoff e redeteção; daemon não cai. | Teste com mock falhando. | [src: FR-3] |
| NFR-5 | Observabilidade | Logs estruturados no journal (detecção, escritas, erros, pausa). | Inspeção do journal. | [inferred] |
| NFR-6 | Portabilidade | Linux, Ubuntu/GNOME Wayland (GNOME 50); sem dependência do X11. | Rodar na sessão atual. | [src: probe 2.2] |
| NFR-7 | Dependências | Somente stdlib + 1 lib TOML; `ddcutil` externo obrigatório. | `go.mod` revisado. | [inferred] |
| NFR-8 | Precisão | Interpolação com erro ≤0,5 ponto vs. fórmula; horário usa relógio local e trata mudança de DST/relógio sem saltos. | Testes de tabela. | [inferred] |
| NFR-9 | Portabilidade | Roda em Linux com `ddcutil`; sem GNOME, o modo noturno usa só DDC e o resto funciona; pré-requisitos ausentes geram instrução específica (pacote, grupo `i2c`, DDC/CI). | Testes de pré-requisitos com fakes; execução em PC sem `gsettings` simulado. | [src: user] |
| NFR-10 | Convenção de idioma | Código, comentários, mensagens da CLI, logs, README e comentários do TOML gerado em inglês; documentos do `.aidlc` seguem a configuração do usuário (pt-BR). | Verificação `just english-check` (sem caracteres não ASCII em `.go`). | [src: user: "inglês no código e comentários"] |

### Constraints · Out of scope · Glossary

- Constraints: Go; `ddcutil` e acesso a `/dev/i2c-*` (o probe funcionou com o usuário atual); usuário não está no grupo `i2c` (A-3); Wayland sem `xrandr` útil.
- Fora do escopo (MVP): GUI/tray, sensor de luz ambiente, geolocalização/nascer-do-sol automático, notebooks com backlight interno, perfis por aplicação, gamma próprio via compositor.
- Glossário: **DDC/CI** protocolo de controle do monitor via I2C; **VCP** código de feature (0x10 brilho); **ponto-chave** par horário→brilho; **rampa** janela de transição do modo noturno; **ganho RGB** VCP 0x16/18/1A.

### Assumptions & open questions

- A-1: nome do binário `dusk`, subcomandos `list|status|pause|resume|reload|install|run`. [inferred]
- A-2: não usar 0x0C/0x14 por variarem entre fabricantes; só ganho RGB. [inferred]
- A-3: acesso I2C continua funcionando sem sudo (probe OK); `install` avisa se não. [inferred]
- A-4: intensidade padrão do modo noturno = azul a 60% e vermelho/verde inalterados; configurável. [inferred]
- A-5: sem retomar estado "paused" após reboot (volta ativo). [inferred]
- A-6: 5 min de redeteção e passo de 1 ponto; configuráveis. [inferred]

## 2.4 User stories

Persona única: **Dono do PC** (uso pessoal, confortável com terminal e TOML).

### US-1 Iniciar sozinho  (FR-1, FR-14)  Persona: Dono do PC
Como dono do PC, quero que o programa suba sozinho a cada login, para não precisar lembrar de executá-lo.
- AC1: Given o serviço instalado com `dusk install`, When reinicio e faço login, Then o serviço está `active` sem comando manual.
- AC2: Given o serviço morreu por erro, When passa o tempo de restart, Then ele volta sozinho.

### US-2 Ver e identificar monitores  (FR-2, FR-3, FR-4)  Persona: Dono do PC
Como dono do PC, quero ver todos os meus monitores com um apelido estável, para configurá-los individualmente.
- AC1: Given AOC, LG e Samsung conectados, When rodo `dusk list`, Then os três aparecem com modelo, serial, porta e se aceitam brilho/RGB.
- AC2: Given que desligo e religo um monitor, When passa a redeteção, Then ele volta à agenda sem reiniciar.
- AC3: Given que troco o cabo de porta, When a redeteção roda, Then a config por modelo+serial segue valendo.

### US-3 Agendar o brilho de cada monitor  (FR-5, FR-6, FR-11, FR-13)  Persona: Dono do PC
Como dono do PC, quero definir horário→brilho por monitor e ver o brilho mudar aos poucos, para não sentir saltos.
- AC1: Given 18:00→40% e 22:00→20%, When são 20:00, Then o brilho é 30%.
- AC2: Given mudo o brilho no botão do monitor, When vem o próximo passo, Then a agenda é reaplicada.
- AC3: Given edito o TOML com erro, When recarrego, Then a config antiga permanece e o erro é mostrado.

### US-4 Modo noturno suave em todos os monitores  (FR-7, FR-8, FR-9, FR-10)  Persona: Dono do PC
Como dono do PC, quero que todos os monitores esquentem a cor em horário configurado com rampa lenta, e voltem ao normal no fim, para proteger meus olhos sem perceber a mudança.
- AC1: Given início 21:00 e rampa 30 min, When são 21:15, Then a intensidade é 50%.
- AC2: Given um monitor sem ganho RGB, When o modo noturno começa, Then o Night Light do GNOME o cobre.
- AC3: Given o fim do modo noturno, When a rampa de saída termina, Then os ganhos RGB originais são restaurados.

### US-5 Pausar o programa  (FR-12)  Persona: Dono do PC
Como dono do PC, quero um comando para pausar/parar a agenda, para usar o monitor manualmente quando eu quiser.
- AC1: Given o daemon ativo, When rodo `dusk pause`, Then não há mais escritas e `dusk status` mostra "paused".
- AC2: Given pausado, When rodo `dusk resume`, Then a agenda volta ao valor correto do horário atual.

### US-6 Configurar pelo assistente  (FR-15, FR-16, FR-17)  Persona: Dono do PC
Como dono do PC, quero rodar um comando de setup que descobre meus monitores e me guia pelas escolhas, para não precisar escrever TOML nem conhecer modelos e seriais.
- AC1: Given nenhuma config, When rodo `dusk setup` e aceito os padrões, Then o arquivo é gravado e validado.
- AC2: Given `ddcutil` ausente ou sem permissão I2C, When rodo `dusk setup`, Then recebo a instrução exata para corrigir.
- AC3: Given uma config existente, When rodo `dusk setup`, Then ela vira backup e seus valores são os padrões.

### US-7 Usar em outro computador  (FR-16, FR-17, NFR-9)  Persona: Dono do PC
Como pessoa que instala o programa em outra máquina, quero que ele funcione com os monitores e o desktop de lá, para não depender do setup do autor.
- AC1: Given outro PC com monitores diferentes, When rodo `dusk setup`, Then ele os lista sem nenhum valor pré-definido.
- AC2: Given um desktop sem GNOME, When o modo noturno começa, Then só o DDC é usado e o aviso aparece no log.

### Story → FR coverage

| FR | Story |
|---|---|
| FR-1, FR-14 | US-1 |
| FR-2, FR-3, FR-4 | US-2 |
| FR-5, FR-6, FR-11, FR-13 | US-3 |
| FR-7, FR-8, FR-9, FR-10 | US-4 |
| FR-12 | US-5 |
| FR-15 | US-6 |
| FR-16, FR-17 | US-6, US-7 |

### Assumptions & open questions
- A-7: uma persona só; sem GUI, logo 2.5 foi pulado. [inferred]

### Amendment 2026-11-13 (pedido do usuário)
- Adicionados FR-15, FR-16, FR-17, NFR-9, NFR-10, US-6 e US-7. O nome do projeto é **dusk** (binário, módulo, serviço, socket, pastas de config e estado).
- O nome antigo do binário nos textos acima (`brightd`) passa a ser `dusk`. FR-9 e FR-17 convergem: `night.fallback` aceita `"auto"` e `"off"` (antes `"gnome"`).
- A-24: o assistente usa só a stdlib (prompts de terminal). [user]
- FR-18 (Should, 2026-11-13, pedido do usuário): `dusk logs [-f] [-n N] [--since TIME]` mostra o log do serviço sem o usuário precisar chamar `journalctl`. Aceite: Given o serviço instalado, When rodo `dusk logs -f`, Then vejo as linhas novas até Ctrl+C (exit 0).
- FR-19 (Must, 2026-11-13, pedido do usuário): nível de cor do modo noturno por monitor. Cada `[[monitor]]` aceita `night = { blue_gain, red_gain, green_gain }` (cada campo opcional, 0-100); o que faltar herda de `[night]`; janela, rampa e fallback continuam globais. `dusk setup` pergunta por monitor com controle de cor. Aceite: Given blue_gain global 60 e um monitor com `night = { blue_gain = 40 }`, When é pleno modo noturno, Then esse monitor fica com azul em 40% do original e os demais em 60%; restaurar o original ao amanhecer não depende do nível usado.
- FR-20 (Should, 2026-11-13, pedido do usuário): `dusk start` inicia o serviço instalado, contraparte de `dusk stop`; confirma que o daemon respondeu; se já estiver rodando não faz nada.
- FR-21 (Should, 2026-11-13, pedido do usuário): `dusk status` mostra a configuração em vigor (arquivo, intervalos, janela e fade do modo noturno, fase atual e próxima mudança, fallback e se está em uso) e, por monitor, brilho, ganhos RGB atuais, alvo e originais salvos, nível noturno (global ou próprio) e agenda. Em modo diurno os ganhos são lidos no máximo a cada 10 minutos, só leitura.
- FR-22 / NFR-3 revisado (2026-11-13, pedido do usuário após travadas): o dusk só conversa com o hardware quando há algo a fazer. Leituras acontecem a cada `verify_interval` (padrão 5m, novo), após falha e ao retomar de `pause`; entre verificações os valores conhecidos são usados e um tick sem mudança não faz nenhuma transação I2C. `rescan_interval` padrão passou de 5m para 30m. Aceite: em 1 hora de ticks de 30 s no platô noturno, no máximo 60 leituras (eram 480).
- Medição que motivou a mudança (hardware real, RTX 4060 com driver proprietário): o serviço usava em média 12,9% de um núcleo (195 s de CPU em 25 min, incluindo os processos ddcutil) e fazia 12 leituras a cada 30 s no modo noturno, mais um `detect` a cada 5 min. A meta original de NFR-3 (<1%) nunca tinha sido medida.

