# Contributing

**English** · [Português](#português)

Thanks for helping. Issues and pull requests can be written in **English or Portuguese**. This repository
holds the Vigil app only (`agent/`, plus the optional `bbport-patch/`).

- **Run and test:** `cd agent; go vet ./...; go test ./...`. CI runs the same on every push and pull
  request, and also builds the Windows tray app. Please do not weaken a test or a security check to make
  it pass: fix the cause, or explain in the pull request why the check is wrong.
- **Commits:** small, with English messages in the form `area: what changed` (for example
  `agent: retry on 429 using Retry-After`).
- **New trophy sources:** a source reads files the emulator or port already writes in plain text, never
  handles keys, only reads, and comes with tests that use synthetic files only. See
  [`docs/SOURCES.md`](docs/SOURCES.md).
- **No game files:** never commit or attach game files, dumps, keys, firmware, third-party assets or
  personal data (including screenshots that show it).
- **License:** contributions are MIT, except in `bbport-patch/` (GPL-2.0-or-later).
- **Security:** see [`SECURITY.md`](SECURITY.md); do not open a public issue for a vulnerability.

---

## Português

Obrigado por ajudar. Issues e pull requests podem ser em **português ou inglês**. Este repositório tem só
o app do Vigil (`agent/`, mais o `bbport-patch/` opcional).

- **Rodar e testar:** `cd agent; go vet ./...; go test ./...`. O CI roda o mesmo a cada push e pull
  request e também compila o app de bandeja do Windows. Não enfraqueça testes nem verificações de
  segurança pra passar: corrija a causa ou explique no PR por que a verificação está errada.
- **Commits:** pequenos, mensagem em inglês no formato `area: o que mudou`.
- **Fontes novas:** leem só arquivos em texto puro que o emulador ou port já grava, nunca mexem com
  chaves, só leem e vêm com testes que usam arquivos sintéticos. Ver
  [`docs/SOURCES.pt-BR.md`](docs/SOURCES.pt-BR.md).
- **Nada de arquivos do jogo:** nunca commite nem anexe arquivos do jogo, dumps, chaves, firmware,
  assets de terceiros ou dados pessoais (inclusive capturas de tela que os mostrem).
- **Licença:** contribuições são MIT, exceto em `bbport-patch/` (GPL-2.0-or-later).
- **Segurança:** veja [`SECURITY.md`](SECURITY.md#português); não abra issue pública pra vulnerabilidade.
