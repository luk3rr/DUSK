# Conventions — DUSK
Status: draft · Confidence: high

- Repo em inglês, ASCII (`just english-check`, exceto a linha `// Author:` do cabeçalho). [mem: M-003]
- Testes nunca tocam hardware, D-Bus ou terminal real; tudo atrás de interfaces com fakes. [mem: M-001]
- DDC só escreve 0x10; 0x16/18/1A apenas no `reset-ddc` confirmado. [mem: M-004]
- Cor/brilho por software, nada fica no hardware. [mem: M-005]
- Arquivos novos têm cabeçalho Filename / Created on / Author. Commits Conventional Commits em inglês, só quando pedido.
