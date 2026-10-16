# Public source scope

This repository publishes the source code of the **Vigil app** only: the agent and tray app
(`agent/`), the optional bbport patch (`bbport-patch/`, GPL-2.0-or-later) and the app's documentation.
The Vigil Trophies website and server are **not** published.

It contains no environment files, keys, passwords, account tokens, user data, backups, firmware, game
executables or extracted game assets. Synthetic test data is part of the source.

Official executables are built by the project's release workflow and published, with SHA256SUMS.txt, in
https://github.com/guutol/vigil-agent-releases. This repository does not build or publish releases.

Updates are reviewed snapshots of the app's tracked source files. Never copy a whole working directory,
.git history, local assets, environment files, runtime data, or the website or server code here.

Live service: https://vigiltrophies.com
