# bbport-patch

Patch pro [bbport](https://github.com/Supermedo/bloodborne_pc) (branch `windows`) que grava cada
troféu desbloqueado numa linha de `user/trophies.jsonl`. É a única parte do Vigil Trophies que roda dentro
do jogo, e ela **não usa rede**: só acrescenta uma linha num arquivo local.

Licença: **GPL-2.0-or-later** (herda do bbport). Ver [`LICENSE-NOTE.md`](LICENSE-NOTE.md).

## O que o patch muda

| Arquivo | Mudança |
|---|---|
| `src/runtime_trophy.h` / `.c` (novos) | `trophy_store_append(user_dir, title_id, trophy_id)`: valida, monta a linha JSON e grava com um único `fwrite` protegido por mutex. Nunca aborta; devolve `-1` em erro. |
| `src/runtime_services.c` | `trophy_unlock` (registrada como `sceNpTrophyUnlockTrophy`) passa a chamar `trophy_store_append`. Mantém o `printf` original, continua devolvendo `0` e `*platinum=-1`. Se gravar falhar, só escreve um aviso no stderr. |
| `src/probe.c` | Passa o `TITLE_ID` lido do `param.sfo` pra `trophy_store_configure` (logo depois do `bbnet_configure`). Sem `param.sfo`, usa `CUSA03173`. |
| `tests/test_trophy.c` (novo) | Teste standalone (não depende do resto do runtime). |
| `build.sh` | Compila e roda o teste no bloco `--test`. |

O `src/runtime_trophy.c` entra no build automaticamente: o `build.sh` e o `gpu/CMakeLists.txt`
(Windows) pegam `src/runtime*.c` por glob.

## Como aplicar

Dentro de um clone do bbport:

```powershell
git checkout windows
git am C:\caminho\para\vigil-trophies\bbport-patch\0001-trophy-store.patch
```

Se o upstream mudou e o `git am` falhar: `git am --abort`, depois `git apply --3way` e resolva
os conflitos (são só 3 linhas fora dos arquivos novos).

## Formato do arquivo

Uma linha por desbloqueio, por exemplo:

```
{"v":1,"title_id":"CUSA03173","trophy_id":12,"unlocked_at":"2026-10-10T06:30:00Z"}
```

Especificação completa em [`FORMAT.md`](FORMAT.md). É o contrato entre o patch e o `vigil-agent`.

## Como testar

Só o teste do patch (não precisa de MSYS2, Vulkan nem SDL). No PowerShell, dentro do clone do
bbport com o patch aplicado (precisa de gcc com winpthreads, ex.: WinLibs POSIX UCRT):

```powershell
New-Item -ItemType Directory -Force out | Out-Null
gcc -std=c11 -O2 -Wall -Wextra -Werror -pthread -Isrc tests/test_trophy.c src/runtime_trophy.c -o out/trophy-test.exe
.\out\trophy-test.exe
```

No Linux: `./build.sh --test` (roda todos os testes do bbport, incluindo este).

O teste cobre: 3 eventos gravados em ordem, rejeição de `title_id` inválido (aspas, vazio, NULL,
minúsculas, tamanho) e de `trophy_id` fora de 0..255, diretório inexistente, configuração do
TITLE_ID e 8 threads × 50 eventos = 400 linhas válidas, sem linha misturada.

Verificado em 2026-10-10 com gcc 16.2.0 (WinLibs MinGW-w64 UCRT POSIX) no Windows 11.
**Não verificado:** build completo do bbport (precisa de MSYS2 CLANG64 + Vulkan + SDL3).

## Como regenerar o patch

O clone do upstream fica em `vendor/bbport` (fora do git). Na branch `vigil-trophy-store`:

```powershell
cd vendor\bbport
git format-patch -1 --stdout > ..\..\bbport-patch\0001-trophy-store.patch
```
