# Changelog

Versões do app **Vigil** (`agent/`), publicadas em https://github.com/guutol/vigil-agent-releases. Formato: [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/).

## [v0.1.0-beta.9] - 2026-10-16

Notas completas: [`docs/release-notes/v0.1.0-beta.9.md`](docs/release-notes/v0.1.0-beta.9.md).

### Incluído

- Fonte `gow3-log` (beta): o `last_run.log` do launcher do port do God of War III (gow3_pc). Detecção
  pelas configurações do launcher, pelo pacote e pelo port em execução, sem varrer o disco; conjunto
  de troféus lido do dump (`param.sfo`, `npbind.dat`, pasta de update primeiro). Sem identificação,
  nada é enviado (nunca como Bloodborne).

### Corrigido

- A pasta de saves do port do God of War III era classificada como `bbport-log` e recebia a pasta do
  jogo do Bloodborne.

## [v0.1.0-beta.5] - 2026-10-14

Notas completas: [`docs/release-notes/v0.1.0-beta.5.md`](docs/release-notes/v0.1.0-beta.5.md).

### Corrigido

- Linhas do `last_run.log` terminadas em `
` (um dos modos do launcher do bbport) não eram
  lidas: a conquista não era enviada nem anunciada.
- Descoberta automática do bbport portátil (`bbport-windows`/`bbport-linux`, antes da primeira
  conquista), sem confundir a pasta `user` com shadPS4; a pasta do jogo do launcher vale também com o
  `user_dir` padrão.
- Instalações antigas recebem uma vez a janela "Fontes encontradas" com o que mudou, trocando fontes
  salvas com o tipo errado se o jogador aceitar.

## [v0.1.0-beta.1] - 2026-10-13

Primeira beta (pré-lançamento). Notas completas, instalação e limitações:
[`docs/release-notes/v0.1.0-beta.1.md`](docs/release-notes/v0.1.0-beta.1.md).

### Incluído

- App de bandeja para Windows (`vigil-agent-tray.exe`): vincular o PC pelo navegador, encontrar fontes,
  sincronizar, notificações, pausar/retomar, iniciar com o Windows, idioma pt-BR/inglês.
- Linha de comando para Windows, Linux (amd64 e arm64) e Steam Deck.
- Fontes: bbport oficial (log, sem patch), bbport com o patch do Vigil, RPCS3 (beta), shadPS4 (beta).
- Fila local com novas tentativas (backoff e `Retry-After`); nada se perde sem servidor.

### Nesta versão

- O build publicado aponta para `https://vigiltrophies.com` (nunca `localhost`); o workflow recusa um
  servidor padrão local.
- `vigil-agent config server https://host`: usar outro servidor HTTPS sem recompilar (apaga o vínculo
  local antigo, para o token não ir a outro endereço).
- O menu do app mostra "Sem conexão com o servidor: N na fila, tentando de novo" em vez de
  "Sincronizando" quando o envio falha.
- `vigil-agent link` explica quando o servidor não responde e como trocar de servidor.

[v0.1.0-beta.1]: https://github.com/guutol/vigil-trophies/releases/tag/v0.1.0-beta.1
[v0.1.0-beta.5]: https://github.com/guutol/vigil-agent-releases/releases/tag/v0.1.0-beta.5
