# agent — `vigil-agent`

Programa pequeno (um `.exe`, sem instalar nada) que roda no PC do jogador. Ele:

1. vincula o PC à conta do Vigil Trophies por um código curto (igual login de TV);
2. lê os troféus que ports e emuladores gravam no disco: **bbport oficial** (o log
   `last_run.log` do launcher, sem patch), **bbport com o patch do Vigil** (`trophies.jsonl`),
   **God of War III** e **Shadow of the Colossus (AjncSystem)** (`last_run.log`, beta),
   **RPCS3** (`TROPUSR.DAT`) e **shadPS4** (XML de troféus);
3. envia os desbloqueios pro servidor, guardando numa fila local se o servidor estiver fora do ar.

O jogo **nunca** fala com a internet; quem envia é este programa, separado. O agent **só lê** os
arquivos dos emuladores: nunca escreve, move ou apaga nada neles, e nunca procura nem usa chaves.
Detalhes de cada fonte: [`docs/SOURCES.md`](../docs/SOURCES.md).

Dois executáveis, mesma configuração:

| Executável | Pra quem | Como é |
|---|---|---|
| `vigil-agent-tray.exe` | **Jogador no Windows** (o padrão da página Download) | Ícone na bandeja, sem console: vincula pelo navegador, acha as fontes sozinho, notifica cada troféu |
| `vigil-agent` / `vigil-agent.exe` | Linux, Steam Deck e quem prefere terminal | Linha de comando (abaixo) |

Os dois usam a mesma pasta (`%AppData%\vigil` / `~/.config/vigil`): token, fila, fontes. Dá pra
alternar entre eles, mas não rodar os dois ao mesmo tempo.

Go; uma dependência só: `golang.org/x/sys` (mantida pelo time do Go), pra bandeja e registro do
Windows. Módulo `github.com/vigil-tracker/agent`.

## App de bandeja (Windows)

Abrir o `vigil-agent-tray.exe` (sem argumentos) põe o ícone do Vigil perto do relógio. Na primeira
vez ele procura as fontes nos lugares de sempre e pergunta **uma vez** se pode acompanhá-las. O menu:

| Item | O que faz |
|---|---|
| *Vinculado como X* / *Não vinculado* | Estado (pergunta ao servidor quem é a conta: `GET /api/agent/me`) |
| **Vincular este PC** | Device flow: abre o navegador direto na página `/link` com o código preenchido; quando você confirma, o app percebe sozinho |
| **Fontes detectadas** | Cada fonte com quantos jogos e troféus tem (clicar abre a pasta); *Procurar fontes de novo*; *Adicionar pasta…* (seletor de pasta do Windows; o tipo é reconhecido pela estrutura da pasta) |
| **Abrir meu perfil** | Abre o seu perfil no site |
| **Pausar / Retomar sincronização** | Para de ler e enviar; ao retomar, relê os arquivos (nada se perde) |
| **Iniciar com o Windows** | Liga/desliga o valor `VigilTrophies` em `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` (só o seu usuário, sem admin) |
| **Ver log** | Abre `tray.log` (na pasta de configuração; rotaciona em 1 MB; nunca contém o token) |
| **Sair** | Fecha o app |

- **Notificações** (menu **Notificações**): só pros desbloqueios achados com o app aberto e aceitos pelo
  servidor (o que já estava nos arquivos ao abrir é enviado sem aviso). Ver a seção abaixo.
- **Uma cópia só** (mutex por usuário): abrir de novo só mostra o menu da que já está rodando.
- **Segurança**: nenhuma porta aberta no PC; a única interface é o menu, diálogos do Windows e o site
  no navegador. O navegador só abre endereços `https://` (ou `http://` local em desenvolvimento) e a
  página de vínculo é validada; "Ver log" e as pastas só abrem pasta ou `.log`, nunca um executável.
  Nada que venha do servidor é executado.
- **Servidor padrão**: `http://localhost:8080` só em builds de desenvolvimento. O build publicado aponta
  para `https://vigiltrophies.com` (o workflow de release usa a variável `VIGIL_DEFAULT_SERVER` ou, sem
  ela, esse endereço, e recusa um padrão local). **O serviço só responde depois do deploy do site**;
  até lá, vincular avisa que não deu pra falar com o servidor. O servidor usado no vínculo fica salvo.
- **Outro servidor HTTPS, sem recompilar**: `vigil-agent config server https://host` (com o
  `vigil-agent-windows-amd64.exe`; a pasta de configuração é a mesma do app de bandeja). Feche e abra o
  app e vincule de novo: trocar de servidor apaga o token local, que nunca é enviado a outro endereço.
  Só `https://` (ou `http://` local em desenvolvimento).
- **Sem conexão**: o menu mostra *Sem conexão com o servidor: N na fila, tentando de novo* em vez de
  *Sincronizando* enquanto o envio falha; a fila é mantida e reenviada sozinha (sem notificação de erro
  repetida).
- **Idioma**: menu **Idioma** → *Automático* (segue o idioma de exibição do Windows:
  português → pt-BR, qualquer outro → inglês), *English* ou *Português (Brasil)*. Vale pra tudo:
  menu, diálogos, cards, notificação do Windows e mensagens de erro; os nomes de troféu e de jogo são
  pedidos ao servidor no idioma escolhido. Textos em `internal/i18n/locales/{en,pt-BR}.json` (mesmas
  chaves, conferido em teste).
- **Fontes encontradas**: janela própria no visual azul-marinho do Vigil, com botões azuis (não a caixa padrão do Windows), uma linha por
  pasta ("bbport (patch) + bbport (log) — 1 jogo, 12 troféus"), emuladores marcados "(beta)", plural
  certo, teclado (Tab, setas, Enter, Esc). Uma pasta só é tida como do bbport com prova (`bbport.ini` na
  pasta ou na de cima, `last_run.log` no formato do bbport, `trophies.jsonl` no formato do patch, ou as
  configurações do próprio launcher); fontes sem jogos só aparecem quando a pasta é certamente daquele
  programa.

Código: `cmd/vigil-agent-tray` (orquestra), `internal/app` (lógica testável: menu, motor de
sincronização, autostart, mensagens), `internal/tray` (Win32: ícone, menu, balões, diálogos),
`internal/notify` (cards de troféu) e `internal/gdip` (desenho com GDI+).

### Notificação de troféu

| Estilo | Como é |
|---|---|
| **Estilo Vigil** (padrão) | Card próprio no canto da tela: ícone do troféu (do servidor; sem ele, um ícone genérico do grau), nome, jogo, grau metálico e faixa azul; a **platina** tem fundo, borda, brilho e som próprios. Entra deslizando, fica ~5 s e sai; **clique** abre a página do jogo no site. Vários troféus aparecem um depois do outro; mais de 3 seguidos viram um resumo (platina sempre aparece sozinha) |
| **Notificação do Windows** | A notificação nativa (balão do ícone da bandeja), com a figura do troféu |
| **Desligadas** | Nada aparece (os troféus continuam indo pro perfil) |

| Estilo Vigil | |
|---|---|
| ![platina](../docs/screenshots/agent/pt-BR/notification-platinum.png) | ![ouro](../docs/screenshots/agent/pt-BR/notification-gold.png) |
| ![prata](../docs/screenshots/agent/pt-BR/notification-silver.png) | ![bronze](../docs/screenshots/agent/pt-BR/notification-bronze.png) |
| ![resumo](../docs/screenshots/agent/pt-BR/notification-summary.png) | |

- Menu **Notificações**: estilo, **Som** (*Desligado / Baixo / Médio / Alto*, padrão *Baixo*; o som é
  suave e normalizado baixo, e fica mudo com o *Não perturbe* / *Assistente de foco* do Windows),
  **Posição** (4 cantos) e **Testar notificação**
  (um troféu de exemplo de cada grau, nada é enviado). Fica salvo no `config.json` (`notifications`).
- Texto legível: tudo em semibold (Segoe UI Variable no Windows 11, Segoe UI antes), nome do troféu com
  18 px e até 2 linhas (reticências no fim), demais textos com no mínimo 13 px; contraste WCAG AA
  (≥ 4,5:1) medido em teste pra cada cor de texto; conferido em 100%, 125% e 150%.
- A janela não rouba o foco (nem com clique), não aparece na barra de tarefas nem no Alt+Tab, fica por
  cima das outras e respeita a escala de DPI. Jogos em **tela cheia exclusiva** escondem qualquer janela
  por cima (limite do Windows): no modo sem borda ou em janela o card aparece.
- **Ícones**: só baixados do servidor configurado (mesmo endereço; redirecionamento pra outro domínio é
  recusado), só PNG válido (até 512 KB e 1024×1024), guardados em `icons/` na pasta de configuração com
  limite de 20 MB.
- **Som** e ícones genéricos são originais, gerados por `internal/notify/assets/make_assets.py`.
- Na linha de comando: `vigil-agent notify-test [--style vigil|windows] [--grade bronze|silver|gold|platinum]`
  (Windows). `--render PASTA [--scale 2] [--lang pt-BR|en]` salva os cards como PNG (assim saíram as
  imagens de `docs/screenshots/agent/`).

## Linha de comando: passo a passo

1. Baixe o agent na página **Download** de https://vigiltrophies.com ou nos
   [downloads oficiais](https://github.com/guutol/vigil-agent-releases/releases) (Windows, Linux, Steam
   Deck), junto com o `SHA256SUMS.txt` para conferir o arquivo.
2. Vincule o PC à sua conta:
   ```powershell
   .\vigil-agent.exe link --server https://SEU-SERVIDOR --name "PC do quarto"
   ```
   Ele mostra um link e um código tipo `ABCD-EFGH`. Abra o link, faça login no site e confirme.
   O agent percebe sozinho quando você aprova.
   `--server` é o endereço do site em produção, sempre com `https://` (ex.:
   `https://meuvigil.duckdns.org`; o site e a API ficam no mesmo domínio). Ele fica salvo no
   `config.json`, então só precisa ser passado no `link`. Sem `--server` e sem config salva, o agent usa
   `http://localhost:8080` (servidor de desenvolvimento).
3. Descubra e adicione as fontes:
   ```powershell
   .\vigil-agent.exe sources detect          # só sugere; não grava nem envia nada
   .\vigil-agent.exe sources add rpcs3 "C:\Emuladores\RPCS3"
   .\vigil-agent.exe sources add bbport-log "C:\Jogos\bbport\user" --game-dir "C:\Games\CUSA03173"
   ```
4. Deixe rodando enquanto joga:
   ```powershell
   .\vigil-agent.exe run
   ```
   Ctrl+C para (o progresso fica salvo). Atalho antigo do bbport, sem configurar nada:
   `.\vigil-agent.exe run --user-dir "C:\Jogos\bbport\user"`.
5. Pra conferir: `.\vigil-agent.exe status`.
6. Pra desvincular: `.\vigil-agent.exe unlink` e revogue o dispositivo em **Configurações →
   Dispositivos** no site.

> `http://` só é aceito pra `localhost`/`127.0.0.1` (testes). Pra qualquer outro servidor, use
> `https://`: o token não pode trafegar sem criptografia.

## Comandos

| Comando | O que faz |
|---|---|
| `link [--server URL] [--name NOME]` | Device flow: mostra o código, faz polling respeitando `interval` e `slow_down`, salva o token. |
| `run [--user-dir PASTA]` | Observa todas as fontes configuradas (mais o bbport em `PASTA`, se passado) a cada 5 s e envia em lotes de até 100. |
| `play bbport [--game-dir PASTA] [--exit-with-game] -- COMANDO ARGS...` | Inicia o bbport oficial (pra quando não há `last_run.log`, ex.: launcher do Linux), mostra a saída do jogo e envia os troféus. O comando é executado direto, sem shell. Ver [`docs/SOURCES.md`](../docs/SOURCES.md). |
| `version` | Mostra a versão (embutida no build de release). |
| `notify-test [--style vigil\|windows] [--grade G]` | Mostra notificações de exemplo (Windows), sem enviar nada. |
| `config catalog-proposals on\|off` | Liga/desliga o envio de catálogos de jogos desconhecidos (padrão: ligado). |
| `status` | Servidor, se está vinculado (sem mostrar o token), fontes, ids conhecidos e itens na fila. |
| `unlink` | Apaga o token local. |
| `sources detect [--in PASTA]...` | Sugere pastas de fontes e mostra quantos troféus cada uma tem. **Só leitura**, não envia nada. `--in` olha mais uma pasta (e as subpastas diretas). |
| `sources add <tipo> <pasta> [--game-dir PASTA] [--update-dir PASTA]` | Adiciona uma fonte: `bbport-log` (pasta `user` do bbport oficial; `--game-dir` = pasta do jogo, que diz o conjunto de troféus), `gow3-log` (pasta de saves do port do God of War III; `--game-dir`/`--update-dir` = pastas do jogo e do update, senão as do launcher do port; sem identificação nada é enviado), `bbport` (pasta `user` com o patch), `rpcs3` (pasta com `dev_hdd0`) ou `shadps4` (pasta `user`, ou a do emulador que a contém). |
| `sources remove <tipo> <pasta>` | Remove uma fonte. |
| `sources list` | Lista as fontes configuradas. |

### Onde o `detect` procura

Nunca varre o disco. Confere uma lista fixa (`Program Files\RPCS3`, `Program Files\shadPS4`,
`%AppData%\rpcs3`, `%AppData%\shadPS4`, `%LocalAppData%\rpcs3`, as pastas equivalentes do Linux e do
macOS e a `user_dir` salva pelo launcher do bbport) e olha **um nível** dentro da pasta do usuário,
Área de Trabalho (inclusive a do OneDrive), Downloads, Documentos, `Games`, Program Files e
`C:\Games`. Uma pasta conta como fonte pelo formato: `dev_hdd0\home` (RPCS3), `trophies.jsonl`
(bbport) ou `home`/`game_data` junto de `sys_modules`/`custom_trophy`/`trophy` (shadPS4).

## Catálogo pela comunidade (ligado por padrão)

Quando um emulador tem a lista de troféus de um jogo que o site ainda **não conhece** (o RPCS3
guarda `TROPCONF.SFM` + PNGs; o shadPS4, `Xml/TROP*.XML` + `Icons/`, os dois em texto puro), o agent
manda essa lista pro servidor (nomes, descrições, graus e ícones; **nenhum** dado seu além do
token). O servidor só publica o jogo quando **3 pessoas diferentes** mandam exatamente o mesmo
conteúdo, e aí os troféus que estavam esperando aparecem no perfil na hora.

- Antes de mandar, o agent pergunta ao servidor quais conjuntos ele já conhece (`/api/agent/catalog-status`)
  e só envia os desconhecidos, no máximo 5 a cada 10 minutos. Cada conjunto é enviado uma vez só
  (`catalogs.json`).
- O `link` avisa disso. Pra desligar: `vigil-agent config catalog-proposals off` (e `on` pra religar);
  o `status` mostra se está ligado.
- O RPCS3 guarda os textos no idioma em que ele está configurado; o agent manda como `en` (o padrão do
  RPCS3). Do shadPS4 vão `en` (`TROP_01`/`TROP`) e `pt-BR` (`TROP_17`) quando existem.
- O agent só lê; nunca descriptografa nada (os arquivos já estão em texto puro porque o próprio
  emulador os gerou).

## Arquivos locais

Ficam em `os.UserConfigDir()/vigil` (Windows: `%AppData%\vigil`; Linux:
`~/.config/vigil`). Dá pra trocar com a variável `VIGIL_CONFIG_DIR`.

| Arquivo | Conteúdo |
|---|---|
| `config.json` | URL do servidor e lista de fontes (`{"server": "...", "sources": [{"type": "rpcs3", "path": "..."}]}`) |
| `token` | Token do agent. Permissão `0600` no Linux. **No Windows** a permissão Unix não existe: a proteção é a da pasta do perfil do usuário (outros usuários comuns do PC não leem, mas administradores e programas rodando como você sim). |
| `known.json` | `event_id`s já enfileirados ou enviados, pra não reenviar ao reler um arquivo. |
| `catalogs.json` | Conjuntos de troféus cujo catálogo já foi proposto (ou que o servidor já conhece). |
| `play/bbport.log` | Saída do jogo iniciado por `play` (reescrito a cada `play`). O `run` também lê esse arquivo. |
| `outbox.jsonl` | Eventos esperando envio. |
| `update.json` | Último resultado da busca por versão nova (data, ETag, versão e endereço oficial do download) e a última versão já avisada. Sem dados pessoais; apagar só faz a próxima busca perguntar de novo. |

O `state.json` das versões antigas não é mais usado (pode ficar lá).

### Atualizar para uma versão nova

Feche o app pelo menu (**Sair**) ou pare o `run`, substitua o executável **no mesmo lugar** e abra de
novo. Nada fica dentro do `.exe`: vínculo, fontes, preferências e a fila continuam nesta pasta, e
"Iniciar com o Windows" continua valendo enquanto o caminho do arquivo for o mesmo (se mudar de pasta,
desmarque e marque de novo no menu). Troféus que estavam na fila são enviados pela versão nova.

### Aviso de versão nova (`internal/update`)

O app de bandeja avisa quando há uma versão mais nova, mas **nunca baixa, troca nem executa nada** e
nunca fecha o jogo ou o próprio Vigil: quem atualiza é o jogador, como descrito acima.

- **Fonte:** a lista pública de releases de [`guutol/vigil-agent-releases`](https://github.com/guutol/vigil-agent-releases)
  (`GET https://api.github.com/repos/guutol/vigil-agent-releases/releases`), que inclui as betas
  (`releases/latest` deixa pré-lançamentos de fora). Rascunhos, tags fora do formato `vX.Y.Z[-pre]`,
  releases sem o arquivo desta plataforma e arquivos ainda não concluídos são ignorados.
- **Comparação:** precedência do semver: `beta.10` > `beta.9`, `v0.1.0` > `v0.1.0-beta.10`. Uma build de
  desenvolvimento (`dev`) nunca recebe aviso.
- **Quando:** 45 s depois de abrir (não atrasa a inicialização nem a primeira sincronização) e a cada
  30 min, mas a resposta fica guardada em `update.json` por 6 h. O pedido seguinte usa `ETag`
  (`If-None-Match`; um 304 não gasta o limite do GitHub). Falha (offline, limite, resposta estranha) vai
  só pro `tray.log`, guarda a última resposta boa e espera 1 h antes de tentar de novo. Cada pedido tem
  15 s de limite. Roda numa goroutine própria: não interfere na sincronização.
- **Aviso:** uma vez por versão, lembrado em `update.json` entre reinícios. No estilo Vigil, um cartão
  azul com o símbolo do Vigil (não um troféu) e sem som; no estilo Windows, a notificação da bandeja; com
  notificações desligadas, só o menu. O menu mostra "Atualização disponível: vX" enquanto houver versão
  nova, e "Verificar atualizações" consulta na hora e responde numa janela.
- **Destino:** só o endereço HTTPS oficial do arquivo desta plataforma,
  `https://github.com/guutol/vigil-agent-releases/releases/download/<tag>/<arquivo>` (bandeja:
  `vigil-agent-tray.exe`; linha de comando: `vigil-agent-windows-amd64.exe`, `vigil-agent-linux-amd64`
  ou `vigil-agent-linux-arm64`). Qualquer outro host, repositório, porta, query ou tag diferente é
  recusado, inclusive se o `update.json` for editado à mão. O link abre no navegador.
- **Linha de comando:** `vigil-agent check-update` faz a mesma consulta e mostra o endereço.

## Como funciona (robustez)

- **Fontes** (`internal/source/...`): cada uma implementa `Name()` e `Scan(ctx) ([]Unlock, error)`
  e devolve todos os troféus desbloqueados que conhece. Um arquivo só é lido de novo quando o
  tamanho ou a data de modificação mudam. Arquivo corrompido gera um aviso **uma vez** (até mudar
  de novo) e não impede as outras fontes.
- **Validação estrita**: limite de tamanho por arquivo (1 MiB pro `TROPUSR.DAT`, 4 MiB pros XML),
  ids de troféu 0..255 sem repetição, ids de conjunto `NPWRnnnnn_nn`, tabelas e offsets checados
  contra o tamanho real. XML com `DOCTYPE`/`ENTITY` é recusado antes de ler (sem XXE); só UTF-8.
  Datas fora de 2000..2100 viram "sem data" (o servidor usa o horário de recebimento).
- **Idempotência**: o `event_id` é **determinístico**: UUIDv5 de `"fonte|trophy_set_id-ou-title_id|trophy_id"`
  (namespace = UUIDv5 da URL `https://github.com/vigil-tracker/vigil/agent/events`). Reler o mesmo
  arquivo, reiniciar o agent ou cair no meio de uma gravação gera sempre o mesmo id, e o servidor
  deduplica por usuário. O evento vai pro `outbox.jsonl` (gravação atômica) **antes** de qualquer envio.
- **bbport**: só linhas completas (terminadas em `\n`), mesma validação do patch (`v == 1`,
  `title_id` `[A-Z0-9]{4,16}`, `trophy_id` 0..255, `unlocked_at` RFC 3339). Arquivo
  truncado/recriado é relido do começo.
- **Conjunto desconhecido**: o servidor guarda como pendente (`pending` no log) e conta quando o
  catálogo daquele jogo existir.
- **Servidor fora do ar / 5xx / rede**: backoff exponencial com jitter, de 1 s até 5 min.
  **429**: espera o `Retry-After`. **401**: para e pede pra rodar `link` de novo (a fila é mantida).
  Evento recusado (`rejected` na resposta) ou lote inteiro com **400**: sai da fila e o motivo vai pro log.
- Eventos que ficaram no `outbox.jsonl` de uma versão antiga (só `title_id`) são enviados como estão.
- Timeout HTTP de 10 s. O token nunca aparece em log nem em mensagem de erro.

## Como rodar e testar (dev)

```powershell
go test ./...
.\build-windows.ps1                 # bin\vigil-agent.exe e bin\vigil-agent-tray.exe, com ícone e versão
go run ./cmd/vigil-agent link --server http://localhost:8080
go run ./cmd/vigil-agent sources detect
go run ./cmd/vigil-agent run --user-dir C:\caminho\do\user
```

Testes (todos com arquivos **sintéticos** criados no próprio teste, nunca arquivos de jogo):

- bbport: linhas válidas, inválidas, repetidas e incompletas; truncamento e arquivo recriado.
- bbport-log: só a linha exata `Runtime: trophy N unlocked` (0..255; linhas parecidas, ids fora
  do intervalo, zeros à esquerda e lixo recusados), CRLF, linha incompleta, log truncado e log
  reescrito pela próxima execução (mesmo cabeçalho), linha longa demais, `param.sfo`/`npbind.dat`
  sintéticos e corrompidos, e que o scan não altera o log. `play`: o próprio binário de teste faz
  o papel do jogo; confere que os argumentos chegam literais (sem shell), que a saída aparece, que
  só o troféu válido é enviado e que o código de saída do jogo é informado.
- gow3-log: identificação pelo dump sintético (base, update com e sem `npbind.dat`, update sem
  `eboot.bin`, outra região pelo título), sem fallback (sem pasta, pasta vazia, dump do Bloodborne,
  sem conjunto, dois conjuntos), CR CR LF, duplicata, linha incompleta, log reescrito/encurtado, o
  `trophies.log` do bbport ignorado, nada enviado nem perdido sem identificação, dois ports com o
  mesmo id de troféu; detecção antes do primeiro jogo, `user_dir` personalizado e vazio, outro disco,
  port em execução, `GOW3_DATA_DIR`, AppImage, nunca como bbport; motor -> API -> notificação com
  fila offline. Nenhum teste com o jogo real.
- RPCS3: `TROPUSR.DAT` montado byte a byte; magic errado, arquivo truncado em vários pontos,
  tabelas demais, contagem absurda, offset fora do arquivo/overflow, id > 255, id repetido, tipo de
  entrada errado, arquivo grande demais, conversão e limites do timestamp, pasta de conjunto
  inválida, conjunto nunca jogado, e que o scan não altera nenhum arquivo.
- shadPS4: layout atual (`home/<id>/trophy/NPWR….xml`) e antigo (`game_data/<TITLE>/…/TROP.XML`),
  `DOCTYPE`/`ENTITY`/DTD externo, entidade não declarada, XML truncado, encoding não UTF-8,
  aninhamento excessivo, ids inválidos/repetidos, timestamps em segundos/milissegundos/lixo,
  `npcommid` diferente do nome do arquivo.
- detect: só pastas óbvias e um nível; nada a dois níveis.
- syncer: várias fontes no mesmo envio, ids determinísticos, reinício sem reenviar, queda antes de
  salvar o `known.json`, outbox antigo, backoff, `Retry-After`, 401, lotes de 100.
- cliente HTTP contra `httptest.Server` (200, 429, 500, 401, erro de rede; `/api/agent/me`; nomes de
  troféu seguindo o redirecionamento 308 da página do jogo, sem mandar o token).
- bandeja (`internal/app`): menu em cada estado (não vinculado, vinculando, sincronizando, pausado,
  token revogado, pasta sumida), autostart com registro falso e com o registro real numa chave de
  teste, instância única (mutex de verdade), cache de nomes com espera após falha, notificações
  (nome, "Troféu #N", resumo), motor (estados, pausa/retomada, 401, desbloqueio "ao vivo"),
  endereços recusados (`file:`, `javascript:`, `http` remoto). `internal/tray`: menu Win32 montado a
  partir do modelo, tamanho da `NOTIFYICONDATAW`, recusa de abrir `.exe`.
- notificações (`internal/notify`): fila (até 3, resumo, platina fora do resumo, resumo que cresce,
  sequência de teste sem juntar), textos, cache de ícones (PNG inválido, grande demais, URL de outro
  domínio, `//outro`, usuário na URL, redirecionamento pra fora, arquivo estragado no disco, limite de
  tamanho), montagem dos cards (nome, grau, ícone, link do jogo, troféu oculto, servidor fora do ar) e,
  no Windows, o desenho de verdade (tamanho por DPI, cantos transparentes, faixa, ícone, PNG quebrado) e
  o ícone pro balão. Preferências salvas (`config`).

## Gerar os binários

No Windows, `.\build-windows.ps1 [-Version v0.2.0] [-Server https://...]` gera os dois `.exe` com
ícone e versão. À mão:

```powershell
New-Item -ItemType Directory -Force bin | Out-Null
go build -trimpath -ldflags "-s -w -X github.com/vigil-tracker/agent/internal/api.Version=dev" -o bin/vigil-agent.exe ./cmd/vigil-agent
$env:GOOS="linux"; $env:GOARCH="amd64"
go build -trimpath -ldflags "-s -w -X github.com/vigil-tracker/agent/internal/api.Version=dev" -o bin/vigil-agent-linux-amd64 ./cmd/vigil-agent
Remove-Item Env:GOOS; Remove-Item Env:GOARCH
```

`bin/` fica fora do git. Os binários oficiais são gerados por um workflow do GitHub Actions a cada
versão e publicados com `SHA256SUMS.txt` em https://github.com/guutol/vigil-agent-releases.

## Diário persistente do bbport (beta.6)

Fontes bbport-log também leem trophies.log ao lado de last_run.log, sem mudar a configuração. O diário usa ID e segundos Unix; registros repetidos e o log de runtime compartilham a identidade de evento existente. A primeira leitura sincroniza histórico sem notificação. O Vigil apenas lê esses arquivos.

Formato confirmado em https://github.com/Supermedo/bloodborne_pc/blob/windows/src/runtime_services.c e launcher/bbport_trophies.py em 15/10/2026. Testes sintéticos cobrem histórico, eventos ao vivo, duplicatas, arquivos reescritos e a prévia de fontes; confirmação em gameplay na instalação afetada ainda é necessária.

## Gerenciar pastas e menu azul (beta.8)

No ícone perto do relógio: Gerenciar pastas → escolha a pasta → Parar de acompanhar. A remoção é confirmada e lembrada; nada é apagado do jogo, do perfil ou da fila. Para desfazer, use a lista de pastas removidas → Voltar a acompanhar. Usar menu do sistema restaura o menu padrão do Windows. Detalhes e prévias em [Menu e pastas](../docs/TRAY-FOLDERS.pt-BR.md).

