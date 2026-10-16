# Vigil — o app

[English](README.md) · **Português**

O Vigil é o app pequeno que roda no PC do jogador para o [Vigil Trophies](https://vigiltrophies.com), um
acompanhador de conquistas para ports de PC e emuladores. Ele vincula o PC a uma conta do Vigil Trophies,
lê os troféus que ports e emuladores já gravam no disco e envia os novos para o perfil do jogador.

Este repositório tem só o código-fonte desse app. O site e o servidor do Vigil Trophies não são
publicados aqui.

## O que tem aqui

| Pasta | O quê |
|---|---|
| [`agent/`](agent/) | O app, em Go: o app de bandeja do Windows (`vigil-agent-tray.exe`) e a linha de comando (`vigil-agent`, Windows, Linux, Steam Deck). Detalhes em [`agent/README.md`](agent/README.md). |
| [`bbport-patch/`](bbport-patch/) | Patch opcional para o bbport que grava cada troféu desbloqueado num arquivo local. GPL-2.0-or-later. |
| [`docs/`](docs/) | Como instalar, quais pastas o app lê ([fontes](docs/SOURCES.pt-BR.md)), notas de versão. |

O app só **lê** os arquivos que ports e emuladores gravam: nunca escreve, move ou apaga nada neles, nunca
mexe com chaves, e o jogo em si nunca fala com a internet.

## Download

As versões oficiais, com `SHA256SUMS.txt`, ficam em
**[guutol/vigil-agent-releases](https://github.com/guutol/vigil-agent-releases/releases)** e na página
Download do site. Guia de instalação: [`docs/INSTALL.pt-BR.md`](docs/INSTALL.pt-BR.md).

## Compilar e testar

```
cd agent
go test ./...
go build ./cmd/vigil-agent
GOOS=windows go build -ldflags "-H windowsgui" ./cmd/vigil-agent-tray
```

No Windows, `agent\build-windows.ps1` gera os dois executáveis com ícone e versão.

## Licença

MIT ([`LICENSE`](LICENSE)), exceto `bbport-patch/` (GPL-2.0-or-later). Problemas de segurança:
[`SECURITY.md`](SECURITY.md). Mudanças por versão: [`CHANGELOG.md`](CHANGELOG.md).
