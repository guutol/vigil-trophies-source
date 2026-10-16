# runtime: record trophy unlocks in a local `user/trophies.jsonl`

## Summary

`sceNpTrophyUnlockTrophy` currently only logs `Runtime: trophy N unlocked`. This PR additionally
appends one JSON line per unlock to `<user>/trophies.jsonl`:

```
{"v":1,"title_id":"CUSA03173","trophy_id":12,"unlocked_at":"2026-10-10T06:30:00Z"}
```

This gives players a persistent, machine-readable record of their progress, and lets optional
companion tools (for example an achievement tracker) read it.

## Scope and guarantees

- **Local only, no network.** The runtime only appends to a file in the user data directory it
  already owns. Nothing is uploaded; any companion tool is a separate, opt-in program.
- **No gameplay effect.** `trophy_unlock` keeps its existing log line and return values
  (`0`, `*platinum = -1`). If the write fails, a warning goes to stderr and the game continues.
- **Never aborts.** Invalid input (title id outside `[A-Z0-9]{4,16}`, trophy id outside `0..255`)
  is rejected without writing; no `exit`, no `assert`.
- **Thread-safe.** Each line is written with a single `fwrite` under a mutex, then flushed.
- **Portable.** `gmtime_r` on Linux, `gmtime_s` on Windows (MinGW-w64 / CLANG64).
- The title id comes from `param.sfo` (`TITLE_ID`), configured in `probe.c` next to
  `bbnet_configure`; `CUSA03173` is the fallback.

## Changes

- `src/runtime_trophy.c` / `.h` (new, picked up by the existing `src/runtime*.c` globs in
  `build.sh` and `gpu/CMakeLists.txt`).
- `src/runtime_services.c`: call `trophy_store_append` from `trophy_unlock`.
- `src/probe.c`: `trophy_store_configure(serial)`.
- `tests/test_trophy.c` + `build.sh --test`: standalone test (three events, invalid input,
  missing directory, title configuration, 8 threads x 50 events -> 400 valid lines).

## Testing

```
gcc -std=c11 -O2 -Wall -Wextra -Werror -pthread -Isrc tests/test_trophy.c src/runtime_trophy.c -o out/trophy-test
out/trophy-test
```

Passes with MinGW-w64 gcc 16.2 on Windows 11. `src/runtime_services.c` passes
`-std=c11 -Wall -Wextra -Werror -fsyntax-only`. A full build of the game binary with this change
has not been run yet.

## License

Contributed under the project's license, GPL-2.0-or-later.
