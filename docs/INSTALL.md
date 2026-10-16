# Install and update the Vigil Agent

**English** · [Português](INSTALL.pt-BR.md)

The agent is the small program that runs on the player's PC. It links the PC to a Vigil Trophies account, reads
the trophies that ports and emulators write to disk ([sources](SOURCES.md)) and sends them to the
server. It only reads those files and never changes them. The game itself never goes online.

## Before you start: what this beta needs

- **The server.** The published build talks to `https://vigiltrophies.com`, online since 2026-10-13.
  If it cannot be reached, linking says so and found trophies wait in a local queue. You can also
  point the agent at another HTTPS server (below).
- **Public downloads.** Download [beta v0.1.0-beta.5](https://github.com/guutol/vigil-agent-releases/releases/tag/v0.1.0-beta.5) without a GitHub account, together with SHA256SUMS.txt. These are unchanged copies of the original Actions build; source is available in this public copy; the original build repository remains private. This beta has no signed build attestation.

| File | For |
|---|---|
| `vigil-agent-tray.exe` | Windows: tray app (recommended) |
| `vigil-agent-windows-amd64.exe` | Windows: command line |
| `vigil-agent-linux-amd64` | Linux (x86-64) |
| `vigil-agent-linux-arm64` | Linux on ARM64 |
| `SHA256SUMS.txt` | SHA-256 of every file above |

Steam Deck uses `vigil-agent-linux-amd64`.

## Check the file before opening it

The executables are **not code-signed** and this beta has **no build provenance attestation** (GitHub
only issues one for public repositories or for GitHub Enterprise Cloud). They were built and published
by [`.github/workflows/release.yml`](../.github/workflows/release.yml) from the tagged commit. Compare
the SHA-256 with `SHA256SUMS.txt`:

```powershell
Get-FileHash .\vigil-agent-tray.exe -Algorithm SHA256
Select-String vigil-agent-tray.exe .\SHA256SUMS.txt
```

```bash
sha256sum --ignore-missing -c SHA256SUMS.txt
```

Windows SmartScreen may say "unknown publisher". Do not turn SmartScreen or your antivirus off. If the hash
matches and you decide to run it, use **More info → Run anyway** for this file only.

## Windows: tray app

1. Put `vigil-agent-tray.exe` in any folder (there is no installer) and open it. An icon appears by the clock.
2. Click **Link this PC**. The browser opens the link page with the code filled in; sign in and confirm.
3. The app finds sources in the usual places and asks **once** whether it may follow them. Later,
   use **Detected sources → Look for sources again** or **Add a folder…**.
4. Leave it running while you play. Each new trophy shows a notification. **Pause syncing**, **Start with
   Windows** (your user only, no admin), language (automatic, English or Portuguese) and **View log**
   are in the menu.

## Linux, Steam Deck or the command line

```bash
chmod +x vigil-agent-linux-amd64
./vigil-agent-linux-amd64 link --server https://SERVER --name "Steam Deck"
./vigil-agent-linux-amd64 sources detect         # only suggests; writes and sends nothing
./vigil-agent-linux-amd64 sources add rpcs3 ~/Emulators/RPCS3
./vigil-agent-linux-amd64 run                    # Ctrl+C stops; progress is kept
```

`--server` is the site address with `https://` (site and API share the domain). It is saved, so it is
needed only for `link`. `http://` is accepted only for `localhost` tests. All commands:
[`agent/README.md`](../agent/README.md) (Portuguese).

## Another HTTPS server

The published build defaults to `https://vigiltrophies.com`. To use your own server without
recompiling, run `vigil-agent config server https://your-server` with the command-line executable.
It uses the same configuration folder as the tray app. Then close and reopen the tray app and link
again. Changing servers deletes the old local link, so the token is never sent to another address.

## Update

1. Close the tray app from its menu (**Quit**) or stop `run`.
2. Replace the executable **in the same place** and open it again.

Link, sources, preferences and the queue live in `%AppData%\vigil` (Linux: `~/.config/vigil`), not in
the executable, so they carry over. **Start with Windows** keeps working as long as the file path
stays the same. There are no automatic updates in this beta.

## Uninstall

Turn off **Start with Windows**, quit the app and delete the executable. To remove the link too:
- delete `%AppData%\vigil`;
- revoke the device in **Settings → Devices** on the site.

## Limitations

- Needs the server reachable to link and sync; not yet tested end to end with a real game.
- No code signing, no provenance attestation, no automatic updates.
- RPCS3 and shadPS4 are beta and were tested only with synthetic files.
- Tray app on Windows only; Linux and Steam Deck use the command line.
- Notifications appear on the main monitor. Exclusive full-screen games hide them; use borderless
  windowed mode.
