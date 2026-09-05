param(
    [switch]$Release,
    [switch]$Test,
    [switch]$Clean
)

$ErrorActionPreference = "Stop"

if ($Clean) {
    if (Test-Path -LiteralPath "bin") {
        Remove-Item -LiteralPath "bin" -Recurse -Force
    }
    go clean -testcache
    return
}

if ($Test) {
    go test ./...
    return
}

New-Item -ItemType Directory -Force -Path "bin" | Out-Null

$ldflags = ""
if ($Release) {
    $ldflags = "-s -w"
}

if ($ldflags) {
    go build -ldflags $ldflags -o "bin\doubletake.exe" ".\cmd\doubletake"
    go build -ldflags $ldflags -o "bin\doubletake-ctl.exe" ".\cmd\doubletake-ctl"
} else {
    go build -o "bin\doubletake.exe" ".\cmd\doubletake"
    go build -o "bin\doubletake-ctl.exe" ".\cmd\doubletake-ctl"
}
