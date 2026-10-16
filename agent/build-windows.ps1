<#
.SYNOPSIS
  Builds vigil-agent.exe (command line) and vigil-agent-tray.exe (tray app, no console window)
  with the Vigil icon and version info, into agent\bin.

.DESCRIPTION
  The Windows resources (icon, version, manifest) come from cmd\*\winres\winres.json and are
  turned into .syso files by go-winres, run with "go run" at a pinned version (nothing is
  installed). The release workflow does the same on Linux.

.EXAMPLE
  .\build-windows.ps1
.EXAMPLE
  .\build-windows.ps1 -Version v0.2.0 -Server https://meuvigil.duckdns.org
#>
param(
    # Version shown by "vigil-agent version" and in the file properties (v1.2.3 or v1.2.3-beta.1)
    [string]$Version = 'v0.0.0-dev',
    # Default server for a fresh install (empty: http://localhost:8080, for development)
    [string]$Server = ''
)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

if ($Version -notmatch '^v(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.]+)?$') { throw "Version must look like v1.2.3: $Version" }
$numeric = "$($Matches[1]).$($Matches[2]).$($Matches[3]).0"
$winres = 'github.com/tc-hib/go-winres@v0.3.3'

foreach ($cmd in 'vigil-agent', 'vigil-agent-tray') {
    & go run $winres make --in "cmd/$cmd/winres/winres.json" --out "cmd/$cmd/rsrc" --arch amd64 `
        --file-version $numeric --product-version $Version.TrimStart('v')
    if ($LASTEXITCODE -ne 0) { throw "go-winres failed for $cmd" }
}

$ldflags = "-s -w -X github.com/vigil-tracker/agent/internal/api.Version=$Version"
if ($Server) { $ldflags += " -X github.com/vigil-tracker/agent/internal/config.DefaultServer=$Server" }
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
New-Item -ItemType Directory -Force bin | Out-Null
& go build -trimpath -ldflags $ldflags -o bin\vigil-agent.exe ./cmd/vigil-agent
if ($LASTEXITCODE -ne 0) { throw 'build failed: vigil-agent' }
& go build -trimpath -ldflags "$ldflags -H windowsgui" -o bin\vigil-agent-tray.exe ./cmd/vigil-agent-tray
if ($LASTEXITCODE -ne 0) { throw 'build failed: vigil-agent-tray' }
Get-ChildItem bin\*.exe | ForEach-Object { '{0}  {1:N1} MB  {2}' -f $_.Name, ($_.Length / 1MB), $_.VersionInfo.FileVersion }
