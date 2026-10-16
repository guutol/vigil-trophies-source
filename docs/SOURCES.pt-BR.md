# Fontes de troféus

[English](SOURCES.md) · **Português**

O `vigil-agent` lê os troféus que ports e emuladores gravam no disco do jogador e envia pro
servidor. Cada lugar de onde ele lê é uma **fonte**. Hoje são cinco:

| Fonte | Plataforma do conjunto | O que o agent lê | Identifica o jogo por |
|---|---|---|---|
| `bbport-log` | `ps4` (o conjunto original) | `user/last_run.log` do launcher do bbport **oficial** (sem patch), ou a saída do jogo via `vigil-agent play` | pasta do jogo (`sce_sys/npbind.dat` e `param.sfo`) |
| `gow3-log` | o conjunto que o dump indica (`ps4`) | `<pasta de saves>/last_run.log` do launcher do port do God of War III (gow3_pc) | pasta do jogo/update (`sce_sys/npbind.dat` e `param.sfo`); nunca chutado |
| `bbport` | `ps4` (o conjunto original) | `user/trophies.jsonl` (bbport com o patch do Vigil) | title id (`CUSA03173`) |
| `rpcs3` | `ps3` | `dev_hdd0/home/<usuário>/trophy/<NPWR…>/TROPUSR.DAT` | conjunto de troféus (nome da pasta) |
| `shadps4` | `ps4` | `user/home/<id>/trophy/<NPWR…>.xml` (e o layout antigo em `game_data`) | conjunto de troféus (nome do arquivo / `<npcommid>`) |

**RPCS3 e shadPS4 estão em beta:** os leitores foram testados só com arquivos sintéticos criados nos
testes (nunca com arquivos de jogo), seguindo o formato que o código de cada emulador grava. Se algo
não for lido como deveria, abra uma issue sem anexar arquivos do jogo.

## Regras que valem pra todas

- **Só leitura.** O agent nunca escreve, move, renomeia ou apaga nada nas pastas dos emuladores.
  O que ele grava fica só na pasta dele (`%AppData%\vigil`).
- **Nada de chaves.** O agent e a ferramenta de importação nunca procuram, baixam, pedem ou usam
  chaves de descriptografia. Eles só leem arquivos que já estão em texto puro.
- **Validação estrita.** Limite de tamanho por arquivo, ids de troféu 0..255 sem repetição, ids de
  conjunto `NPWRnnnnn_nn`, tabelas binárias checadas contra o tamanho real do arquivo, XML com
  `DOCTYPE`/`ENTITY` recusado antes de ler (sem XXE). Arquivo inválido gera um aviso e é ignorado;
  as outras fontes continuam.
- **Datas** são informativas: fora de 2000..2100 viram "sem data", e o servidor ainda confere
  contra o próprio relógio.
- **Polling a cada 5 s**, mas um arquivo só é relido quando o tamanho ou a data de modificação mudam.
- **`event_id` determinístico**: UUIDv5 de `"fonte|conjunto-ou-title-id|troféu"`. Reler o mesmo
  arquivo (ou reiniciar o agent) gera os mesmos ids, e o servidor deduplica por usuário.

## Como configurar

**No Windows, com o app de bandeja** (`vigil-agent-tray.exe`): nada a digitar. Na primeira vez ele
procura as fontes nos mesmos lugares do `sources detect` e pergunta se pode acompanhá-las; depois,
**Fontes detectadas → Procurar fontes de novo** ou **Adicionar pasta…** (o tipo é reconhecido pela
estrutura da pasta: `dev_hdd0` = RPCS3, `user` com `home/`/`trophy/` = shadPS4, `trophies.jsonl` =
bbport com patch, `last_run.log` = bbport oficial). O menu mostra quantos jogos e troféus cada fonte tem.

**Pela linha de comando** (Linux, Steam Deck ou quem preferir):

```powershell
vigil-agent sources detect                 # sugere pastas; só lê, não grava nem envia nada
vigil-agent sources add rpcs3 "C:\Emuladores\RPCS3"
vigil-agent sources add shadps4 "C:\Emuladores\shadPS4"
vigil-agent sources list
vigil-agent run
```

A detecção só oferece o que tem **evidência real**: uma pasta chamada "Bloodborne" não basta pra ser
bbport (precisa do `bbport.ini`, de um `last_run.log` do bbport ou de um `trophies.jsonl` válido), e
fontes incertas sem nenhum jogo não aparecem na pergunta da bandeja. Fontes na mesma pasta aparecem
juntas, com quantos jogos e troféus cada uma tem.

O `detect` **não varre o disco**: confere uma lista fixa de pastas de instalação (Program Files,
`%AppData%`, `%LocalAppData%`, as equivalentes de Linux/macOS e as pastas dos launchers do
bbport e do God of War III) e olha só **um nível** dentro da pasta do usuário, Área de Trabalho,
Downloads, Documentos e `Games` de cada disco fixo (`C:\Games`, `D:\Games`...). Pra olhar outra pasta: `vigil-agent sources detect --in D:\Emuladores`.

## bbport oficial (`bbport-log`, sem patch)

O bbport oficial já imprime uma linha quando o jogo desbloqueia um troféu
(`src/runtime_services.c`, função `trophy_unlock`):

```
Runtime: trophy 12 unlocked
```

O jogo desliga o buffer do stdout (`setvbuf(stdout, NULL, _IONBF, 0)` no `probe.c`), então a
linha sai na hora.

**Windows:** o launcher oficial (`launcher/bbport_launcher_win.py`) já grava toda a saída do jogo
em **`<pasta de saves>/last_run.log`**, tanto pela janela do launcher quanto pelo `--play` (atalhos
e Steam). A pasta de saves é a `user_dir` do `%APPDATA%\bbport-launcher\settings.json` (padrão:
a pasta `user` ao lado do port). O arquivo é **reescrito a cada execução** (`'w'`).

- Pasta pra configurar: a `user_dir`, mais a pasta do jogo pra saber o conjunto:
  `vigil-agent sources add bbport-log "C:\...\user" --game-dir "C:\Games\CUSA03173"`.
  O `sources detect` lê o `settings.json` do launcher e já sugere as duas.
- A pasta do jogo dá o conjunto: `sce_sys/npbind.dat` (não é criptografado) tem o
  `NPWR05818_00` e o `sce_sys/param.sfo` tem o title id. Sem pasta do jogo, o agent usa o padrão do
  bbport (`CUSA03173`, o mesmo do patch) e o servidor converte pelo catálogo.
- O agent lê o log incrementalmente e aceita **só** a linha exata, com regex ancorada
  `^Runtime: trophy (0|[1-9][0-9]{0,2}) unlocked$` e id 0..255 (aceita fim de linha `\r\n` e também `\r\r\n`, que um dos modos do launcher grava).
  Linhas parecidas, ids fora do intervalo e zeros à esquerda são ignorados.
- Reescrita/troca do arquivo: o agent guarda o hash dos 256 bytes antes do ponto onde parou; se
  esses bytes mudarem (nova execução, mesmo que maior que a anterior) ou o arquivo ficar menor, ele
  relê do começo. Os `event_id` são determinísticos, então nada é enviado duas vezes.
- **Horário**: o log não tem data; o `unlocked_at` é o momento em que o agent leu a linha.
- **Limitações**: como o launcher reescreve o log a cada execução, se o agent **não** estiver
  rodando e você abrir o jogo de novo, os troféus da sessão anterior que ainda não tinham sido lidos
  se perdem. Deixe o agent rodando enquanto joga. Com a opção "fechar o launcher ao jogar"
  (`close_on_play`) ligada, a janela do launcher fecha e o log pode parar de ser gravado; use o
  `--play` (atalho/Steam), que continua gravando, ou deixe a opção desligada.

**Linux / Steam Deck:** o launcher do Linux (`bbport_launcher.py`) só mostra a saída numa janela e
não grava arquivo. Nesse caso o agent inicia o jogo:

```bash
vigil-agent play bbport --game-dir /caminho/CUSA03173 -- bash /caminho/do/bbport/run.sh
```

- O comando depois de `--` é executado **direto, sem shell** (programa e argumentos separados);
  nada é interpretado.
- A saída do jogo vai pra um arquivo na pasta do **agent** (`~/.config/vigil/play/bbport.log`,
  reescrito a cada `play`), nunca na pasta do port. O agent mostra essa saída no terminal e lê os
  troféus dela.
- O jogo roda numa sessão própria (`setsid`; no Windows, grupo de processos próprio e sem o console
  do agent): Ctrl+C ou fechar o terminal do agent **não** derruba o jogo, e como ele escreve num
  arquivo (não num pipe), nunca trava ou morre por o agent ter saído. O `vigil-agent run` também lê
  esse arquivo, então o que ficou pra trás é enviado depois.
- Quando o jogo fecha, o agent avisa o código de saída e volta ao normal (continua observando as
  outras fontes). Com `--exit-with-game`, ele envia o que falta e sai.
- O `run.sh` do port recebe as configurações do launcher por variáveis de ambiente; o `play` usa o
  ambiente do terminal. Se você muda opções no launcher, rode o jogo pelo launcher uma vez ou exporte
  as variáveis antes.

## Port do God of War III (`gow3-log`, gow3_pc)

O port de PC do God of War III Remastered ([gow3_pc](https://github.com/FelipePrado0/gow3_pc))
imprime a mesma linha do bbport quando o jogo desbloqueia um troféu (`src/runtime_services.c`,
`trophy_unlock`), e o launcher dele grava a saída do jogo em **`<pasta de saves>/last_run.log`**
(reescrito a cada execução, pela janela do launcher e com `--play`). O jogo desliga o buffer do
stdout no `main()`, então a linha sai na hora. Isso foi conferido no código do port (tag
`windows-v2.4.0`), ainda não numa partida de verdade.

- **Detecção** (sem varrer o disco): as configurações do launcher do port
  (`%APPDATA%\gow3-launcher\settings.json`, ou `$XDG_CONFIG_HOME/gow3-launcher/settings.json` /
  `~/.config/...` no Linux) dão `game_dir`, `update_dir` e `user_dir`; `user_dir` vazio é a pasta
  `user` ao lado do port. O agent também reconhece o próprio pacote (`gow3-windows`, com
  `bin/gow3-probe.exe`) nas pastas de sempre, ao lado da pasta do jogo configurada (outro disco), um
  port em execução, `GOW3_DATA_DIR` e a pasta de dados do AppImage (`~/.local/share/gow3`). Ele é
  encontrado **antes do primeiro troféu**, e pastas que você parou de acompanhar não voltam a ser
  oferecidas.
- **Identificação**: o conjunto vem do dump, do jeito que o launcher lê: a pasta de update quando
  tem `eboot.bin`, senão a pasta do jogo; o `sce_sys/param.sfo` precisa ser do God of War III
  (`CUSA01623`, `CUSA01715`, `CUSA01720`, ou título com "God of War III") e o
  `sce_sys/npbind.dat` diz o conjunto `NPWR…`. Nada é chutado: **sem isso, nada é enviado** (a pasta
  mostra "Jogo não identificado"), e o log nunca é enviado como Bloodborne. As linhas continuam no
  arquivo e são lidas quando a pasta do jogo for definida (no launcher do port ou com
  `--game-dir`/`--update-dir`).
- Manual: `vigil-agent sources add gow3-log "C:\...\gow3-windows\user" --game-dir "D:\Games\CUSA01623"`
  (e `--update-dir`, se o launcher tiver um).
- A leitura é a mesma do `bbport-log`: só a linha exata, finais `\r\r\n`, linha incompleta
  esperada, log reescrito/trocado/encurtado lido do começo, `event_id` determinístico (sem
  duplicar), fila offline. Um `trophies.log` ao lado não é lido (esse diário é do bbport).
- **Data**: o log não tem data, então `unlocked_at` é a hora em que o agent leu a linha.
- **Catálogo**: os eventos vão pro conjunto que o dump indica. O catálogo já tem o conjunto de PS4
  `NPWR08268_00` ("God of War III Remastered", plataforma `ps4`), e os desbloqueios do port contam
  nele com "Jogado em: port de PC". Se o seu dump indicar outro conjunto (outra região), os
  desbloqueios ficam pendentes até o catálogo desse conjunto ser adicionado e aprovado; nada é juntado
  pelo nome, e o jogo de PS3 (RPCS3) continua sendo outro conjunto. O port não consegue propor
  catálogo sozinho (o arquivo de troféus dele é criptografado).
- **Limitações**:
  - Deixe o agent aberto enquanto joga: o launcher reescreve o log a cada execução, e um log antigo
    sobrescrito não tem como ser recuperado.
  - Com a opção do launcher "fechar o launcher ao jogar" ligada, o botão JOGAR **não** grava log.
    Desligue a opção, ou abra com `--play` (atalho), que sempre grava.

## bbport com o patch do Vigil (`bbport`)

O patch do bbport (`bbport-patch/`) acrescenta uma linha por troféu em `user/trophies.jsonl`
(formato em [`bbport-patch/FORMAT.md`](../bbport-patch/FORMAT.md)):

```json
{"v":1,"title_id":"CUSA03173","trophy_id":12,"unlocked_at":"2026-10-10T06:30:00Z"}
```

- Pasta pra configurar: a `user` do port (`sources add bbport ...\user`), ou o atalho
  `vigil-agent run --user-dir ...\user`.
- O arquivo só cresce; o agent lê incrementalmente e só linhas completas.
- O evento vai com `title_id`; o servidor converte pro conjunto pelo catálogo
  (`CUSA03173` → `NPWR05818_00`).

## RPCS3 (PS3)

O RPCS3 guarda o progresso de cada conjunto em `TROPUSR.DAT`, um arquivo binário big-endian
(referência: `rpcs3/Loader/TROPUSR.h` no código do RPCS3):

```
cabeçalho (0x30): magic 0x818F54AD, unk1, tables_count, unk2, reserved[32]
tabelas (0x20 cada): type, entries_size, unk1, entries_count, offset (u64), reserved (u64)
tabela tipo 6, entradas de 0x70 bytes (o arquivo grava 0x60 = sem o cabeçalho de 16 bytes da entrada):
    entry_type=6, entry_size, entry_id, unk, trophy_id, trophy_state (1 = desbloqueado),
    unk4, unk5, timestamp1 (u64), timestamp2 (u64), unk6[64]
```

- Timestamps são `CellRtcTick`: microssegundos desde 0001-01-01 UTC. O agent usa o `timestamp2`
  (o mesmo que o RPCS3 lê) e converte: `unix = (tick − 62135596800000000) / 1e6`.
- O id do conjunto vem do **nome da pasta** (`NPWR01234_00`), validado pelo padrão.
- Pasta pra configurar: a do RPCS3 (a que tem `dev_hdd0`). Também aceita `dev_hdd0`, uma pasta
  `home/<usuário>` ou a pasta `trophy`. Todos os usuários de 8 dígitos dentro de `home` são lidos.
- Conjunto instalado mas nunca jogado (sem `TROPUSR.DAT`) é ignorado em silêncio.
- **Limitação**: muitos jogos de PS3 não têm platina; o catálogo desses conjuntos vem sem
  `platinum_id` e o servidor nunca cria platina pra eles.
- Catálogo (nomes e ícones): `tools/trophy-import` lê o `TROPCONF.SFM` (XML puro) e os PNGs da
  mesma pasta. O `TROPCONF.SFM` está no idioma em que o RPCS3 estava configurado.

## shadPS4 (PS4)

O shadPS4 grava o estado dos troféus em XML:

```
<pasta user>/home/<id do usuário>/trophy/NPWR05818_00.xml          (versões atuais)
<pasta user>/game_data/<TITLE_ID>/TrophyFiles/trophy00/Xml/TROP.XML (versões antigas)
```

```xml
<trophyconf>
  <npcommid>NPWR05818_00</npcommid>
  <trophy id="012" ... unlockstate="true" timestamp="1760077800">...</trophy>
</trophyconf>
```

- `timestamp` em segundos Unix (milissegundos também são aceitos).
- No layout atual o conjunto vem do nome do arquivo e tem que bater com o `<npcommid>` quando ele
  existe; no antigo vem do `<npcommid>`, e o title id do nome da pasta.
- A pasta `user` fica onde o shadPS4 é executado (modo portátil). Se ela não existir, o shadPS4 usa
  `%AppData%\shadPS4` no Windows, `~/.local/share/shadPS4` no Linux e
  `~/Library/Application Support/shadPS4` no macOS.
- Pasta pra configurar: a `user` (ou a do emulador que contém `user`).
- **Limitação importante**: esses XML só existem porque o próprio shadPS4 descriptografou os
  dados de troféu do jogo com a chave de troféus que **o usuário configurou no shadPS4**. O Vigil
  não tem, não pede e não usa essa chave: sem ela configurada no emulador, não há o que ler.
- Catálogo: `tools/trophy-import --from shadps4 <user>/trophy/<NPWR…>` lê `Xml/TROP_01.XML` ou
  `Xml/TROP.XML` (inglês), `Xml/TROP_17.XML` (pt-BR) e `Icons/`.

## O que não é suportado

- **Ports sem troféus originais.** Se o port não chama a API de troféus do jogo original (ou não
  imprime nem grava nada quando ela é chamada), não há o que ler. Pra esses casos o caminho é o port adotar o formato do
  `trophies.jsonl` (ver `bbport-patch/FORMAT.md`).
- Emuladores que guardam troféus criptografados sem uma versão em texto puro.
- Contas oficiais (PSN, Xbox, Steam): não há suporte, sem promessa.

## Onde foi jogado (`played_on`)

Cada desbloqueio guarda a fonte (`origin`) e, a partir dela, onde o jogo rodou: `bbport`,
`bbport-log` e `gow3-log` → **port de PC** (`pc-port`), `rpcs3` → **emulador no PC (PS3)** (`pc-emulator-ps3`),
`shadps4` → **emulador no PC (PS4)** (`pc-emulator-ps4`). O site mostra o selo "Jogado em" em cada
troféu do perfil e na lista de quem platinou, e a página do jogo filtra por lugar. Uma fonte nova
precisa dizer o seu `played_on` em `PlayedOn.fromSource` (servidor).

## Jogo que o catálogo ainda não conhece

Se o agent envia um conjunto que não está no catálogo do servidor, o evento **não é perdido**:
fica em `pending_unlocks` (limite de 2000 por usuário e 50 conjuntos desconhecidos) e o perfil
mostra um card "Jogo desconhecido (NPWR…)" com a contagem. Quando o catálogo daquele conjunto é
adicionado, os pendentes viram desbloqueios normais **na hora**, com as mesmas regras de platina e
de suspeita.

O catálogo pode chegar de três jeitos:

1. **Pela comunidade** (RPCS3 e shadPS4): o agent lê a lista de troféus que o emulador já guarda em
   texto puro (`TROPCONF.SFM` + PNGs; `Xml/TROP*.XML` + `Icons/`) e manda uma proposta. O servidor
   publica quando 3 usuários diferentes elegíveis (conta com 1 dia ou mais e algum desbloqueio
   verificado) mandam conteúdo idêntico; ícones são validados e re-encodados. Ligado por padrão;
   `vigil-agent config catalog-proposals off` desliga.
2. **Por um admin**: a página `/admin/catalog` mostra as propostas pendentes (inclusive as
   divergentes) pra publicar ou rejeitar.
3. **Pelo dono do servidor**: `tools/trophy-import` gera o catálogo local a partir dos próprios
   arquivos; depois, "Recarregar catálogo do disco" no admin (sem reiniciar).

## Como adicionar uma fonte nova

1. Crie `agent/internal/source/<nome>/` com um tipo que implemente `source.Source`:
   ```go
   Name() string                                   // "meuemu", vai no campo "source" do evento
   Scan(ctx context.Context) ([]source.Unlock, error)
   ```
   `Scan` devolve **todos** os troféus desbloqueados que a fonte conhece (o syncer descarta os já
   enviados). Use `source.Cache` pra só reler arquivos que mudaram e `source.ReadLimited` com um
   limite de tamanho realista. Preencha `TrophySetID` (preferível) e/ou `TitleID`.
2. Só leitura, sempre. Valide tudo que vem do arquivo e nunca inclua conteúdo do arquivo em mensagens
   de erro.
3. Registre o tipo em `source.Types`, no `openSource` de `cmd/vigil-agent/main.go` e, se der pra
   reconhecer a pasta pelo formato, em `detect.Classify`.
4. No servidor, acrescente o nome em `IngestService.SOURCES`, no `CHECK` de `pending_unlocks.source`
   (nova migração Flyway; exemplo: `V6__bbport_log_source.sql`) e o lugar correspondente em
   `PlayedOn.fromSource`. Se a fonte tiver catálogo legível, implemente `catalog.Provider`
   (`CatalogDirs()`) no agent e aceite a fonte em `ProposalValidator.SOURCES`.
5. Testes só com arquivos **sintéticos** criados no próprio teste: válido, corrompido, truncado,
   grande demais, datas estranhas. Nunca arquivos de jogo.
6. Documente aqui e em [`SOURCES.md`](SOURCES.md) (inglês).
