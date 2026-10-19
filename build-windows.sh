#!/usr/bin/env bash
# Cross-compiles myrdp.exe and the session helper it starts for each RDP tab,
# on Linux. Both end up in build/bin and must be copied to Windows together.
set -euo pipefail
cd "$(dirname "$0")"

missing=()
for cmd in go npm; do
    command -v "$cmd" >/dev/null || missing+=("$cmd")
done

# go install puts wails in $(go env GOPATH)/bin, which is often not on PATH.
wails=$(command -v wails || true)
if [[ -z $wails ]] && command -v go >/dev/null && [[ -x $(go env GOPATH)/bin/wails ]]; then
    wails=$(go env GOPATH)/bin/wails
fi
[[ -n $wails ]] || missing+=("wails (go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0)")

if (( ${#missing[@]} )); then
    echo 'Cannot build: these are missing:' >&2
    printf '  %s\n' "${missing[@]}" >&2
    exit 1
fi

"$wails" build -platform windows/amd64
# Wails writes the generated bindings as executable on Linux; they are committed as 644.
find frontend/wailsjs -type f -exec chmod 644 {} +

# -H windowsgui: the helper has no console window of its own.
GOOS=windows GOARCH=amd64 go build -ldflags '-H windowsgui' -o build/bin/myrdp-session.exe ./cmd/myrdp-session

echo 'Built build/bin/myrdp.exe and build/bin/myrdp-session.exe'
