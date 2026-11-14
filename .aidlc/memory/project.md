# Project memory
<!-- Approved rules only. One bullet per rule, newest last. Retire, don't delete. -->
- M-001 · 2026-11-13 · src: Inception retrospective · Rule: Testes jamais tocam hardware real; chamadas a ddcutil/gsettings passam só por adaptadores mockáveis. · Why: evitar alterar monitores reais durante testes.
- M-002 · 2026-11-13 · src: Inception retrospective · Rule: Escritas DDC apenas nos VCP 0x10, 0x16, 0x18 e 0x1A (brilho e ganho RGB), sempre com clamp. · Why: outras features variam por fabricante e podem desconfigurar o monitor.
- M-003 · 2026-11-13 · src: user request (UNIT-0 amendment) · Rule: Everything in the repository (code, comments, CLI/log messages, README, generated TOML comments) is written in English; only the .aidlc documents follow the user's language setting (pt-BR). · Why: the project must be usable by people on other computers.
