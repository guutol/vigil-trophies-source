# Trophy sources

**English** · [Português](SOURCES.pt-BR.md)

`vigil-agent` reads the trophies that ports and emulators write to the player's disk and sends them to
the server. Each place it reads from is a **source**. There are four today:

| Source | Trophy set platform | What the agent reads | Identifies the game by |
|---|---|---|---|
| `bbport-log` | `ps4` (the original set) | `user/last_run.log` from the **official** bbport launcher (no patch), or the game output via `vigil-agent play` | game folder (`sce_sys/npbind.dat` and `param.sfo`) |
| `bbport` | `ps4` (the original set) | `user/trophies.jsonl` (bbport with the Vigil patch) | title id (`CUSA03173`) |
| `rpcs3` | `ps3` | `dev_hdd0/home/<user>/trophy/<NPWR…>/TROPUSR.DAT` | trophy set (folder name) |
| `shadps4` | `ps4` | `user/home/<id>/trophy/<NPWR…>.xml` (and the older layout under `game_data`) | trophy set (file name / `<npcommid>`) |

**RPCS3 and shadPS4 are beta:** their readers were tested only with synthetic files created by the
tests, never with game files. They follow the format each emulator's own code writes. If something is
not read as it should be, open an issue without attaching game files.

## Rules for every source

- **Read-only.** The agent never writes, moves, renames or deletes anything in emulator folders. What
  it writes stays in its own folder (`%AppData%\vigil`).
- **No keys.** The agent and the import tool never look for, download, ask for or use decryption keys.
  They only read files that are already plain text.
- **Strict validation:**
  - a size limit per file;
  - trophy ids 0..255 with no repeats and set ids `NPWRnnnnn_nn`;
  - binary tables checked against the real file size;
  - XML with `DOCTYPE`/`ENTITY` refused before parsing (no XXE).

  An invalid file produces a warning and is skipped; the other sources keep working.
- **Dates** are informative. Dates outside 2000..2100 become "no date", and the server also checks them
  against its own clock.
- **Polling every 5 s**, but a file is read again only when its size or modification time changes.
- **Deterministic `event_id`**: a UUIDv5 of `"source|set-or-title-id|trophy"`. Re-reading the same file
  (or restarting the agent) yields the same ids, and the server de-duplicates per user.

## Setting sources up

**On Windows, with the tray app** (`vigil-agent-tray.exe`) there is nothing to type. The first time,
it looks in the same places as `sources detect` and asks whether it may follow what it found. Later,
use **Detected sources → Look for sources again** or **Add a folder…**. The type is recognized from
the folder's structure:
- `dev_hdd0` means RPCS3;
- `user` with `home/` or `trophy/` means shadPS4;
- `trophies.jsonl` means patched bbport;
- `last_run.log` means official bbport.

The menu shows how many games and trophies each source has.

**From the command line** (Linux, Steam Deck, or if you prefer):

```bash
vigil-agent sources detect                 # suggests folders; only reads, never writes or sends
vigil-agent sources add rpcs3 "C:\Emulators\RPCS3"
vigil-agent sources add shadps4 "C:\Emulators\shadPS4"
vigil-agent sources list
vigil-agent run
```

Detection only offers what has **real evidence**. A folder named "Bloodborne" is not enough to count
as bbport: it needs a `bbport.ini`, a bbport `last_run.log` or a valid `trophies.jsonl`. Uncertain
sources with no games are not offered by the tray. Sources in the same folder are shown together, with
how many games and trophies each one has.

`detect` **does not scan the disk**. It checks a fixed list of install folders: Program Files,
`%AppData%`, `%LocalAppData%`, their Linux and macOS equivalents, and the bbport launcher's
`user_dir`. It looks only **one level** deep into the user folder, Desktop, Downloads, Documents and
`C:\Games`. To look in another folder: `vigil-agent sources detect --in D:\Emulators`.

## Official bbport (`bbport-log`, no patch)

Official bbport already prints a line when the game unlocks a trophy (`src/runtime_services.c`,
function `trophy_unlock`):

```
Runtime: trophy 12 unlocked
```

The game turns off stdout buffering (`setvbuf(stdout, NULL, _IONBF, 0)` in `probe.c`), so the line is
written immediately.

**Windows:** the official launcher (`launcher/bbport_launcher_win.py`) already writes all game output
to **`<saves folder>/last_run.log`**, both from the launcher window and with `--play` (shortcuts and
Steam). The saves folder is the `user_dir` in `%APPDATA%\bbport-launcher\settings.json`; by default
it is the `user` folder next to the port. The file is **rewritten on every run** (`'w'`).

- Folder to configure: the `user_dir`, plus the game folder so the set is known:
  `vigil-agent sources add bbport-log "C:\...\user" --game-dir "C:\Games\CUSA03173"`. `sources detect`
  reads the launcher's `settings.json` and suggests both.
- The game folder gives the set: `sce_sys/npbind.dat` (not encrypted) holds `NPWR05818_00` and
  `sce_sys/param.sfo` holds the title id. Without a game folder the agent uses bbport's default
  (`CUSA03173`, the same as the patch), and the server converts it through the catalog.
- The agent reads the log incrementally and accepts **only** the exact line: the anchored regex
  `^Runtime: trophy (0|[1-9][0-9]{0,2}) unlocked$` with an id of 0..255 (`\r\n` line endings
  accepted, and `\r\r\n`, which one of the launcher's ways of starting the game writes). It ignores similar lines, out-of-range ids and leading zeros.
- File rewritten or replaced: the agent keeps a hash of the 256 bytes before the point where it
  stopped. If those bytes change (a new run, even a longer one) or the file shrinks, it reads from the
  start again. Since `event_id`s are deterministic, nothing is sent twice.
- **Time**: the log has no date, so `unlocked_at` is when the agent read the line.
- **Limitations**:
  - The launcher rewrites the log on every run. If the agent is **not** running and you start the
    game again, trophies from the previous session that were not read yet are lost. Keep the agent
    running while you play.
  - With the launcher's "close the launcher when playing" option (`close_on_play`) on, the launcher
    window closes and the log may stop being written. Use `--play` (shortcut or Steam), which keeps
    writing it, or leave that option off.

**Linux / Steam Deck:** the Linux launcher (`bbport_launcher.py`) only shows the output in a window and
writes no file. In that case the agent starts the game:

```bash
vigil-agent play bbport --game-dir /path/CUSA03173 -- bash /path/to/bbport/run.sh
```

- The command after `--` runs **directly, without a shell** (program and arguments separately);
  nothing is interpreted.
- The game output goes to a file in the **agent's** folder (`~/.config/vigil/play/bbport.log`,
  rewritten on every `play`), never in the port's folder. The agent shows that output in the terminal
  and reads the trophies from it.
- The game runs in its own session: `setsid` on Linux; on Windows, its own process group without the
  agent's console. Ctrl+C or closing the agent's terminal does **not** kill the game. Because the game
  writes to a file and not a pipe, it never hangs or dies when the agent exits. `vigil-agent run`
  also reads that file, so anything left behind is sent later.
- When the game exits, the agent reports the exit code and goes back to normal, still watching the
  other sources. With `--exit-with-game` it sends what is left and exits.
- The port's `run.sh` gets the launcher's settings through environment variables, and `play` uses the
  terminal's environment. If you change options in the launcher, run the game from the launcher once,
  or export the variables first.

## bbport with the Vigil patch (`bbport`)

The bbport patch (`bbport-patch/`) appends one line per trophy to `user/trophies.jsonl` (format in
[`bbport-patch/FORMAT.md`](../bbport-patch/FORMAT.md)):

```json
{"v":1,"title_id":"CUSA03173","trophy_id":12,"unlocked_at":"2026-10-10T06:30:00Z"}
```

- Folder to configure: the port's `user` folder (`sources add bbport ...\user`), or the shortcut
  `vigil-agent run --user-dir ...\user`.
- The file only grows; the agent reads it incrementally and only complete lines.
- The event carries `title_id`; the server converts it to the set through the catalog
  (`CUSA03173` → `NPWR05818_00`).

## RPCS3 (PS3) — beta

RPCS3 stores each set's progress in `TROPUSR.DAT`, a big-endian binary file (reference:
`rpcs3/Loader/TROPUSR.h` in RPCS3's source):

```
header (0x30): magic 0x818F54AD, unk1, tables_count, unk2, reserved[32]
tables (0x20 each): type, entries_size, unk1, entries_count, offset (u64), reserved (u64)
table type 6, 0x70-byte entries (the file stores 0x60 = without the entry's 16-byte header):
    entry_type=6, entry_size, entry_id, unk, trophy_id, trophy_state (1 = unlocked),
    unk4, unk5, timestamp1 (u64), timestamp2 (u64), unk6[64]
```

- Timestamps are `CellRtcTick`: microseconds since 0001-01-01 UTC. The agent uses `timestamp2` (the one
  RPCS3 reads) and converts it: `unix = (tick − 62135596800000000) / 1e6`.
- The set id comes from the **folder name** (`NPWR01234_00`), checked against the pattern.
- Folder to configure: RPCS3's folder (the one with `dev_hdd0`). It also accepts `dev_hdd0`, a
  `home/<user>` folder or the `trophy` folder. Every 8-digit user inside `home` is read.
- A set that is installed but never played (no `TROPUSR.DAT`) is silently skipped.
- **Limitation**: many PS3 games have no platinum. Their catalog has no `platinum_id`, and the server
  never awards a platinum for them.
- Catalog (names and icons): `tools/trophy-import` reads `TROPCONF.SFM` (plain XML) and the PNGs in the
  same folder. `TROPCONF.SFM` is in the language RPCS3 was set to.

## shadPS4 (PS4) — beta

shadPS4 stores trophy state as XML:

```
<user folder>/home/<user id>/trophy/NPWR05818_00.xml                  (current versions)
<user folder>/game_data/<TITLE_ID>/TrophyFiles/trophy00/Xml/TROP.XML  (older versions)
```

```xml
<trophyconf>
  <npcommid>NPWR05818_00</npcommid>
  <trophy id="012" ... unlockstate="true" timestamp="1760077800">...</trophy>
</trophyconf>
```

- `timestamp` in Unix seconds (milliseconds are accepted too).
- In the current layout the set comes from the file name and must match `<npcommid>` when present. In
  the older one it comes from `<npcommid>`, and the title id from the folder name.
- The `user` folder is where shadPS4 runs (portable mode). If it does not exist, shadPS4 uses
  `%AppData%\shadPS4` on Windows, `~/.local/share/shadPS4` on Linux and
  `~/Library/Application Support/shadPS4` on macOS.
- Folder to configure: `user` (or the emulator folder that contains `user`).
- **Important limitation**: these XML files exist only because shadPS4 itself decrypted the game's
  trophy data with the trophy key that **the user configured in shadPS4**. Vigil does not have, ask for
  or use that key: without it configured in the emulator, there is nothing to read.
- Catalog: `tools/trophy-import --from shadps4 <user>/trophy/<NPWR…>` reads:
  - `Xml/TROP_01.XML` or `Xml/TROP.XML` (English);
  - `Xml/TROP_17.XML` (Brazilian Portuguese);
  - `Icons/`.

## Not supported

- **Ports without the original trophies.** If a port does not call the original game's trophy API, or
  prints and writes nothing when it is called, there is nothing to read. For those, the way forward is
  for the port to adopt the `trophies.jsonl` format (see `bbport-patch/FORMAT.md`).
- Emulators that store trophies encrypted with no plain-text copy.
- Official accounts (PSN, Xbox, Steam) and any other emulator or port. Nothing beyond the four sources
  above is implemented, with no promise of more.

## Where it was played (`played_on`)

Each unlock stores its source (`origin`) and, from it, where the game ran:

| Source | `played_on` | Shown as |
|---|---|---|
| `bbport`, `bbport-log` | `pc-port` | PC port |
| `rpcs3` | `pc-emulator-ps3` | PC emulator (PS3) |
| `shadps4` | `pc-emulator-ps4` | PC emulator (PS4) |

The site shows a "Played on" badge on each profile trophy and in the platinum list, and the game page
filters by it. A new source must declare its `played_on` in `PlayedOn.fromSource` (server).

## A game the catalog does not know yet

If the agent sends a set that is not in the server's catalog, the event is **not lost**. It is kept in
`pending_unlocks` (up to 2000 per user and 50 unknown sets), and the profile shows an "Unknown game
(NPWR…)" card with the count. As soon as that set's catalog is added, the pending events become normal
unlocks, with the same platinum and suspicion rules.

The catalog can arrive in three ways:

1. **From the community** (RPCS3 and shadPS4). The agent reads the trophy list the emulator already
   keeps in plain text (`TROPCONF.SFM` + PNGs; `Xml/TROP*.XML` + `Icons/`) and submits a proposal.
   - The server publishes it when 3 different eligible users send identical content. Eligible means
     an account at least 1 day old with some verified unlock.
   - Icons are validated and re-encoded.
   - On by default; `vigil-agent config catalog-proposals off` turns it off.
2. **By an admin**: the `/admin/catalog` page lists pending proposals (including conflicting ones) to
   publish or reject.
3. **By the server owner**: `tools/trophy-import` builds the local catalog from your own files. Then
   use "Reload catalog from disk" in the admin page (no restart).

## Adding a new source

1. Create `agent/internal/source/<name>/` with a type that implements `source.Source`:
   ```go
   Name() string                                   // "myemu", sent in the event's "source" field
   Scan(ctx context.Context) ([]source.Unlock, error)
   ```
   `Scan` returns **every** unlocked trophy the source knows about; the syncer drops the ones already
   sent.
   - Use `source.Cache` to re-read only files that changed, and `source.ReadLimited` with a realistic
     size limit.
   - Fill in `TrophySetID` (preferred) and/or `TitleID`.
2. Always read-only. Validate everything that comes from the file, and never include file content in
   error messages.
3. Register the type in `source.Types` and in `openSource` in `cmd/vigil-agent/main.go`. If the folder
   can be recognized by its format, also register it in `detect.Classify`.
4. On the server:
   - add the name to `IngestService.SOURCES`;
   - add it to the `CHECK` on `pending_unlocks.source`, in a new Flyway migration (example:
     `V6__bbport_log_source.sql`);
   - add the matching place in `PlayedOn.fromSource`.

   If the source has a readable catalog, implement `catalog.Provider` (`CatalogDirs()`) in the agent
   and accept the source in `ProposalValidator.SOURCES`.
5. Tests only with **synthetic** files created by the test itself: valid, corrupted, truncated, too
   large, odd dates. Never game files.
6. Document it here and in [`SOURCES.pt-BR.md`](SOURCES.pt-BR.md).
