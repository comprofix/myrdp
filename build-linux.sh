#!/usr/bin/env bash
# Builds myrdp and the session helper it starts for each RDP tab, on Linux.
# Both end up in build/bin and must stay side by side.
set -euo pipefail
cd "$(dirname "$0")"

missing=()
for cmd in go npm gcc pkg-config; do
    command -v "$cmd" >/dev/null || missing+=("$cmd")
done

# go install puts wails in $(go env GOPATH)/bin, which is often not on PATH.
wails=$(command -v wails || true)
if [[ -z $wails ]] && command -v go >/dev/null && [[ -x $(go env GOPATH)/bin/wails ]]; then
    wails=$(go env GOPATH)/bin/wails
fi
[[ -n $wails ]] || missing+=("wails (go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0)")

if command -v pkg-config >/dev/null; then
    for pkg in gtk+-3.0 webkit2gtk-4.1; do
        pkg-config --exists "$pkg" || missing+=("$pkg (development files)")
    done
fi

if (( ${#missing[@]} )); then
    echo 'Cannot build: these are missing:' >&2
    printf '  %s\n' "${missing[@]}" >&2
    echo 'Packages: Arch gtk3 webkit2gtk-4.1; Debian/Ubuntu libgtk-3-dev libwebkit2gtk-4.1-dev;' >&2
    echo '          Fedora gtk3-devel webkit2gtk4.1-devel. Wails also needs Go, Node.js/npm and gcc.' >&2
    exit 1
fi

# webkit2_41: distributions now ship WebKitGTK 4.1 only; Wails defaults to 4.0.
"$wails" build -tags webkit2_41
# Wails writes the generated bindings as executable on Linux; they are committed as 644.
find frontend/wailsjs -type f -exec chmod 644 {} +

# The helper has no Linux backend until cmd/myrdp-session has Linux source.
if [[ $(go list -e -f '{{len .GoFiles}}' ./cmd/myrdp-session) == 0 ]]; then
    echo 'Skipped build/bin/myrdp-session: the session helper has no Linux backend yet, so sessions will not start.'
    echo 'Built build/bin/myrdp'
    exit 0
fi

go build -o build/bin/myrdp-session ./cmd/myrdp-session

echo 'Built build/bin/myrdp and build/bin/myrdp-session'
