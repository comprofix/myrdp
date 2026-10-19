# Builds myrdp.exe and the session helper it starts for each RDP tab.
# Both end up in build\bin and must stay side by side.
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

wails build
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# -H windowsgui: the helper has no console window of its own.
go build -ldflags '-H windowsgui' -o build\bin\myrdp-session.exe .\cmd\myrdp-session
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host 'Built build\bin\myrdp.exe and build\bin\myrdp-session.exe'
