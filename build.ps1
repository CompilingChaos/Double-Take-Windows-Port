param(
    [switch]$Release,
    [switch]$Test,
    [switch]$Clean,
    [string]$Version = ""
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

if (-not $Version) {
    $Version = (git describe --tags --always --dirty 2>$null)
    if (-not $Version) {
        $Version = "dev"
    }
}

$ldflags = ""
if ($Release) {
    $ldflags = "-s -w"
}

# The desktop app records this version when it installs itself.
$guiLdflags = "$ldflags -X main.version=$Version -H windowsgui".Trim()

if ($ldflags) {
    go build -ldflags $guiLdflags -o "bin\doubletake.exe" ".\cmd\doubletake-gui"
    go build -ldflags $ldflags -o "bin\doubletake-cli.exe" ".\cmd\doubletake"
    go build -ldflags $ldflags -o "bin\doubletake-ctl.exe" ".\cmd\doubletake-ctl"
    go build -ldflags $ldflags -o "bin\doubletake-test-receiver.exe" ".\cmd\doubletake-test-receiver"
} else {
    go build -ldflags $guiLdflags -o "bin\doubletake.exe" ".\cmd\doubletake-gui"
    go build -o "bin\doubletake-cli.exe" ".\cmd\doubletake"
    go build -o "bin\doubletake-ctl.exe" ".\cmd\doubletake-ctl"
    go build -o "bin\doubletake-test-receiver.exe" ".\cmd\doubletake-test-receiver"
}
