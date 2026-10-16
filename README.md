# Vigil — the app

**English** · [Português](README.pt-BR.md)

Vigil is the small app that runs on a player's PC for [Vigil Trophies](https://vigiltrophies.com), an
achievement tracker for PC ports and emulators. It links the PC to a Vigil Trophies account, reads the
trophies that ports and emulators already write to disk, and sends new ones to the player's profile.

This repository holds the source code of that app only. The Vigil Trophies website and server are not
published here.

## What is here

| Folder | What |
|---|---|
| [`agent/`](agent/) | The app, in Go: the Windows tray app (`vigil-agent-tray.exe`) and the command line (`vigil-agent`, Windows, Linux, Steam Deck). Details (in Portuguese) in [`agent/README.md`](agent/README.md). |
| [`bbport-patch/`](bbport-patch/) | An optional patch for bbport that writes each unlocked trophy to a local file. GPL-2.0-or-later. |
| [`docs/`](docs/) | How to install, which folders the app reads ([sources](docs/SOURCES.md)), release notes. |

The app only **reads** the files ports and emulators write: it never writes, moves or deletes anything in
them, never touches keys, and the game itself never talks to the internet.

## Download

Official builds, with `SHA256SUMS.txt`, are at
**[guutol/vigil-agent-releases](https://github.com/guutol/vigil-agent-releases/releases)** and on the
website's Download page. Install guide: [`docs/INSTALL.md`](docs/INSTALL.md).

## Build and test

```
cd agent
go test ./...
go build ./cmd/vigil-agent
GOOS=windows go build -ldflags "-H windowsgui" ./cmd/vigil-agent-tray
```

On Windows, `agent\build-windows.ps1` builds both executables with their icon and version.

## License

MIT ([`LICENSE`](LICENSE)), except `bbport-patch/` (GPL-2.0-or-later). Security issues:
[`SECURITY.md`](SECURITY.md). Changes per version: [`CHANGELOG.md`](CHANGELOG.md).
