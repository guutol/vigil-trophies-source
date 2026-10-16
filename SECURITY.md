# Security policy — Vigil app

**English** · [Português](#português)

## Reporting a vulnerability

**Do not open a public issue.** Use this repository's GitHub Security Advisory: the **Security** tab →
**Report a vulnerability**. This opens a private advisory that only the maintainers can see. Problems
with the Vigil Trophies website or service can be reported the same way, or by e-mail to
vigiltrophies@gmail.com.

If you can, include the affected part (`vigil-agent`, the tray app, the patch), steps to reproduce or a
proof of concept, and the impact you expect. The first reply should come within a few days.

## Scope

- The app (`agent/`) and the patch (`bbport-patch/`).
- Out of scope: bbport itself (report it in its own repository) and attacks that require administrator
  access to the player's computer.

## How the app protects the player

Summarized in `agent/README.md` (Portuguese). In particular: the app only reads the files ports and
emulators write; the account token is never written to the log; links open only official `https://`
addresses; updates are announced but never downloaded or run by themselves; the game never accesses the
network.

---

## Português

### Como reportar uma vulnerabilidade

**Não abra uma issue pública.** Use o GitHub Security Advisory deste repositório: aba **Security** →
**Report a vulnerability**. Isso abre um aviso privado que só os mantenedores veem. Problemas no site ou
no serviço do Vigil Trophies podem ser reportados do mesmo jeito, ou pelo e-mail
vigiltrophies@gmail.com.

Se puder, diga a parte afetada (`vigil-agent`, o app de bandeja, o patch), como reproduzir ou uma prova
de conceito, e o impacto esperado. A primeira resposta deve vir em poucos dias.

### Escopo

- O app (`agent/`) e o patch (`bbport-patch/`).
- Fora do escopo: o próprio bbport (reporte no repositório dele) e ataques que exigem acesso de
  administrador ao computador do jogador.

### Como o app protege o jogador

Resumo em `agent/README.md`. Em especial: o app só lê os arquivos que ports e emuladores gravam; o token
da conta nunca vai para o log; links abrem só endereços `https://` oficiais; atualizações são avisadas,
nunca baixadas ou executadas sozinhas; o jogo nunca acessa a rede.
