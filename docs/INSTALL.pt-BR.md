# Instalar e atualizar o Vigil Agent

[English](INSTALL.md) · **Português**

O agent é o programa pequeno que roda no PC do jogador. Ele vincula o PC a uma conta do Vigil
Trophies, lê os troféus que ports e emuladores gravam no disco ([fontes](SOURCES.pt-BR.md)) e envia
pro servidor. Ele só lê esses arquivos e nunca os altera. O jogo em si nunca acessa a internet.

## Antes de começar: o que esta beta precisa

- **Do servidor.** O build publicado fala com `https://vigiltrophies.com`, no ar desde 13/10/2026. Se
  ele não responder, vincular avisa, e os troféus encontrados esperam numa fila local. Dá pra apontar
  o agent pra outro servidor HTTPS (abaixo).
- **Download público.** Baixe a [beta v0.1.0-beta.5](https://github.com/guutol/vigil-agent-releases/releases/tag/v0.1.0-beta.5) sem conta no GitHub, junto com SHA256SUMS.txt. Os arquivos são cópias idênticas do build original do Actions; o código está disponível nesta cópia pública; o repositório do build original segue privado. Esta beta não tem atestado assinado de proveniência.

| Arquivo | Pra quê |
|---|---|
| `vigil-agent-tray.exe` | Windows: app de bandeja (recomendado) |
| `vigil-agent-windows-amd64.exe` | Windows: linha de comando |
| `vigil-agent-linux-amd64` | Linux (x86-64) |
| `vigil-agent-linux-arm64` | Linux em ARM64 |
| `SHA256SUMS.txt` | SHA-256 de todos os arquivos acima |

O Steam Deck usa o `vigil-agent-linux-amd64`.

## Confira o arquivo antes de abrir

Os executáveis **não têm assinatura de código**, e esta beta **não tem atestado de proveniência do
build** (o GitHub só emite pra repositórios públicos ou no GitHub Enterprise Cloud). Eles foram
compilados e publicados pelo [`.github/workflows/release.yml`](../.github/workflows/release.yml) a
partir do commit da tag. Compare o SHA-256 com o `SHA256SUMS.txt`:

```powershell
Get-FileHash .\vigil-agent-tray.exe -Algorithm SHA256
Select-String vigil-agent-tray.exe .\SHA256SUMS.txt
```

```bash
sha256sum --ignore-missing -c SHA256SUMS.txt
```

O SmartScreen do Windows pode avisar "editor desconhecido". Não desligue o SmartScreen nem o
antivírus. Se o hash bater e você decidir abrir, use **Mais informações → Executar assim mesmo** só
pra este arquivo.

## Windows: app de bandeja

1. Coloque o `vigil-agent-tray.exe` em qualquer pasta (não há instalador) e abra. O ícone aparece perto do relógio.
2. Clique em **Vincular este PC**. O navegador abre a página de vínculo com o código preenchido; entre na conta e confirme.
3. O app procura as fontes nos lugares de sempre e pergunta **uma vez** se pode acompanhá-las. Depois,
   use **Fontes detectadas → Procurar fontes de novo** ou **Adicionar pasta…**.
4. Deixe aberto enquanto joga; cada troféu novo mostra uma notificação. No menu ficam **Pausar**,
   **Iniciar com o Windows** (só o seu usuário, sem admin), o idioma (automático, inglês ou
   português) e **Ver log**.

## Linux, Steam Deck ou linha de comando

```bash
chmod +x vigil-agent-linux-amd64
./vigil-agent-linux-amd64 link --server https://SERVIDOR --name "Steam Deck"
./vigil-agent-linux-amd64 sources detect         # só sugere; não grava nem envia nada
./vigil-agent-linux-amd64 sources add rpcs3 ~/Emuladores/RPCS3
./vigil-agent-linux-amd64 run                    # Ctrl+C para; o progresso fica salvo
```

`--server` é o endereço do site, com `https://` (o site e a API ficam no mesmo domínio). Ele fica salvo,
então só precisa ser passado no `link`. `http://` só é aceito pra testes em `localhost`. Todos os
comandos: [`agent/README.md`](../agent/README.md).

## Outro servidor HTTPS

O build publicado usa `https://vigiltrophies.com`. Pra usar o seu servidor sem recompilar, rode
`vigil-agent config server https://seu-servidor` com o executável de linha de comando, que usa a mesma
pasta de configuração do app de bandeja. Depois feche e abra o app de bandeja e vincule de novo. Trocar
de servidor apaga o vínculo local antigo, pra que o token nunca vá a outro endereço.

## Atualizar

1. Feche o app pelo menu (**Sair**) ou pare o `run`.
2. Substitua o executável **no mesmo lugar** e abra de novo.

Vínculo, fontes, preferências e a fila ficam em `%AppData%\vigil` (Linux: `~/.config/vigil`), fora do
executável, e continuam. **Iniciar com o Windows** continua valendo enquanto o caminho do arquivo for o
mesmo. Não há atualização automática nesta beta.

## Desinstalar

Desligue **Iniciar com o Windows**, saia do app e apague o executável. Pra apagar também o vínculo:
- apague `%AppData%\vigil`;
- revogue o dispositivo em **Configurações → Dispositivos** no site.

## Limitações

- Precisa do servidor acessível pra vincular e sincronizar; ainda não testado de ponta a ponta com um jogo de verdade.
- Sem assinatura de código, sem atestado de proveniência, sem atualização automática.
- RPCS3 e shadPS4 estão em beta e foram testados só com arquivos sintéticos.
- App de bandeja só no Windows; no Linux e no Steam Deck, linha de comando.
- As notificações aparecem no monitor principal. Jogos em tela cheia exclusiva escondem o card; use
  janela sem borda.
