# myrdp

A tabbed remote desktop manager. Windows first; Linux (FreeRDP) is planned.

Licensed under the GNU General Public License, version 3 or later. See `LICENSE`.

## Build

Requires Go, Node.js and the Wails v2 CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`).

```
.\build-windows.ps1
```

This produces `build\bin\myrdp.exe` and `build\bin\myrdp-session.exe`. Keep them side by side: the main
program starts one `myrdp-session.exe` per open RDP tab.

For development, `wails dev` gives live reload. Build the session helper first so it exists in `build\bin`.

## How it fits together

| Path | Purpose |
|---|---|
| `main.go`, `app.go` | Wails entry point and the API the UI calls |
| `frontend/` | The UI (React + TypeScript) |
| `cmd/myrdp-session` | Helper process that hosts one RDP session and embeds it in a tab |
| `internal/rdpax` | Hosts Microsoft's RDP ActiveX control (`mstscax.dll`) |
| `internal/session` | Platform-neutral contract between the manager and a session backend |
| `internal/sessions` | Starts helpers and keeps their windows positioned over their tabs |
| `internal/model` | Connection tree, file format and the list of settings |
| `internal/core` | Tree editing and inheritance |
| `internal/store` | Storage interface and the JSON file backend |
| `internal/secrets` | Passwords in the OS keyring |
| `internal/config` | Per-machine program settings |

## Configuration

Files live in `%APPDATA%\myrdp` (or the directory given by `--config <dir>` or the `MYRDP_CONFIG`
environment variable):

- `connections.json`: the connection tree. A folder or connection stores only the settings it changes;
  everything else is inherited from its parent folder, then `defaults`, then the built-in defaults.
- `settings.json`: per-machine program settings.
- `backups\`: a copy of `connections.json` taken each time the program starts (the last 10 are kept).

Passwords are never written to these files. They are stored in the OS keyring (Windows Credential Manager).
