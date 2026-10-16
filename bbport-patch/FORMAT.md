# `trophies.jsonl` — formato versão 1

Contrato entre o patch do bbport (quem escreve) e o `vigil-agent` (quem lê).

## Local

`<user_dir>/trophies.jsonl`, onde `<user_dir>` é a pasta de dados do usuário do bbport
(`user/` por padrão, ou a passada em `--user`).

## Codificação

- UTF-8 (na prática só ASCII), sem BOM.
- Uma linha por evento, terminada em `\n` (LF, nunca CRLF: o arquivo é aberto em modo binário).
- O arquivo só cresce (append). O escritor nunca reescreve nem apaga linhas.

## Linha

```
{"v":1,"title_id":"CUSA03173","trophy_id":12,"unlocked_at":"2026-10-10T06:30:00Z"}
```

Sem espaços, campos sempre nessa ordem (o leitor não deve depender da ordem, mas pode contar com ela).

| Campo | Tipo | Regra |
|---|---|---|
| `v` | inteiro | Sempre `1` nesta versão. Leitor deve ignorar linhas com outra versão. |
| `title_id` | string | `[A-Z0-9]{4,16}` (ex.: `CUSA03173`). |
| `trophy_id` | inteiro | `0..255`. |
| `unlocked_at` | string | UTC, `YYYY-MM-DDTHH:MM:SSZ` (RFC 3339, sem fração). Relógio do PC do jogador: **só informativo**. |

## Garantias do escritor

- Cada linha é escrita com um único `fwrite` sob mutex, seguido de `fflush`: dentro do mesmo
  processo, linhas nunca se misturam.
- Entradas inválidas não são gravadas.
- **Não há deduplicação.** O mesmo troféu pode aparecer mais de uma vez (o jogo pode chamar o
  unlock de novo). Quem deduplica é o servidor.
- Falha de escrita não afeta o jogo: o evento é perdido e um aviso vai pro stderr.

## Regras pro leitor

- Ler só linhas completas (terminadas em `\n`). Uma linha sem `\n` no fim pode estar sendo escrita.
- Linha que não for JSON válido ou não seguir a tabela acima: pular e seguir.
- O arquivo pode ser apagado ou recriado pelo jogador. O leitor deve detectar truncamento
  (tamanho menor que o offset salvo ou começo do arquivo diferente) e reler do início.
- Campos desconhecidos numa linha `v:1` devem ser ignorados (permite adicionar campos opcionais
  sem mudar a versão).
