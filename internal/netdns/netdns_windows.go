//go:build windows

package netdns

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Security model
//
// Changing DNS servers needs administrator rights. DoubleTake never elevates
// itself; it asks Windows to run the Microsoft-signed PowerShell from the
// system directory with one of the two fixed scripts below. Nothing from the
// user's profile, the environment, the network or a file is spliced into
// them, so there is nothing to inject into the elevated process:
//
//   - The scripts are constants, passed with -EncodedCommand.
//   - -NoProfile skips profile scripts, and the first statement points
//     PSModulePath at the system module folder only, so a module planted in a
//     user-writable folder cannot shadow the DnsClient or NetTCPIP cmdlets.
//   - The previous settings are saved by the elevated script itself under
//     HKLM\SOFTWARE\DoubleTake\CloudflareDNS, which only administrators can
//     write. Restoring reads them back from there and accepts only values
//     that parse as IP addresses.
//   - Cloudflare is used over DNS over HTTPS where Windows supports it, with
//     plaintext fallback switched off, so lookups are encrypted.

// scriptPrelude runs first in every script DoubleTake hands to PowerShell.
const scriptPrelude = `
$ErrorActionPreference = 'Stop'
$env:PSModulePath = Join-Path ([Environment]::SystemDirectory) 'WindowsPowerShell\v1.0\Modules'
$cloudflare = @('1.1.1.1', '1.0.0.1', '2606:4700:4700::1111', '2606:4700:4700::1001')
$dohTemplate = 'https://cloudflare-dns.com/dns-query'
$root = 'HKLM:\SOFTWARE\DoubleTake\CloudflareDNS'
function Get-ConnectedAdapters {
    @(Get-NetIPConfiguration | Where-Object {
        ($_.IPv4DefaultGateway -or $_.IPv6DefaultGateway) -and $_.NetAdapter.Status -eq 'Up'
    })
}
function Get-Servers([int] $index) {
    $v4 = @((Get-DnsClientServerAddress -InterfaceIndex $index -AddressFamily IPv4 -ErrorAction SilentlyContinue).ServerAddresses)
    $v6 = @((Get-DnsClientServerAddress -InterfaceIndex $index -AddressFamily IPv6 -ErrorAction SilentlyContinue).ServerAddresses)
    @($v4 + $v6 | Where-Object { $_ })
}
function Get-StaticServers([string] $guid, [string] $stack) {
    $path = "HKLM:\SYSTEM\CurrentControlSet\Services\$stack\Parameters\Interfaces\$guid"
    "$((Get-ItemProperty -LiteralPath $path -Name NameServer -ErrorAction SilentlyContinue).NameServer)"
}
function Test-UsesCloudflare([int] $index) {
    $v4 = @((Get-DnsClientServerAddress -InterfaceIndex $index -AddressFamily IPv4 -ErrorAction SilentlyContinue).ServerAddresses)
    $v4.Count -gt 0 -and $cloudflare -contains $v4[0]
}
`

// statusScript lists the connected adapters and their resolvers. It needs no
// administrator rights.
const statusScript = scriptPrelude + `
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$result = @(Get-ConnectedAdapters | ForEach-Object {
    [pscustomobject]@{ Index = $_.InterfaceIndex; Alias = $_.InterfaceAlias; Current = @(Get-Servers $_.InterfaceIndex) }
})
ConvertTo-Json -InputObject $result -Compress -Depth 4
`

// enableScript runs elevated. It saves each adapter's own settings once, so
// switching on twice never overwrites the original backup with Cloudflare.
const enableScript = scriptPrelude + `
$adapters = Get-ConnectedAdapters
if ($adapters.Count -eq 0) { exit 3 }
New-Item -Path "$root\Adapters" -Force | Out-Null
New-Item -Path "$root\DoH" -Force | Out-Null
foreach ($adapter in $adapters) {
    $index = $adapter.InterfaceIndex
    if (Test-UsesCloudflare $index) { continue }
    $guid = [string] $adapter.NetAdapter.InterfaceGuid
    $saved = "$root\Adapters\$guid"
    if (-not (Test-Path -LiteralPath $saved)) {
        New-Item -Path $saved -Force | Out-Null
        New-ItemProperty -LiteralPath $saved -Name StaticV4 -PropertyType String -Value (Get-StaticServers $guid 'Tcpip') -Force | Out-Null
        New-ItemProperty -LiteralPath $saved -Name StaticV6 -PropertyType String -Value (Get-StaticServers $guid 'Tcpip6') -Force | Out-Null
    }
    Set-DnsClientServerAddress -InterfaceIndex $index -ServerAddresses $cloudflare
}
if (Get-Command Set-DnsClientDohServerAddress -ErrorAction SilentlyContinue) {
    foreach ($server in $cloudflare) {
        $saved = "$root\DoH\" + $server.Replace(':', '_')
        $entry = Get-DnsClientDohServerAddress -ServerAddress $server -ErrorAction SilentlyContinue
        if (-not (Test-Path -LiteralPath $saved)) {
            New-Item -Path $saved -Force | Out-Null
            New-ItemProperty -LiteralPath $saved -Name Existed -PropertyType DWord -Value ([int] [bool] $entry) -Force | Out-Null
            if ($entry) {
                New-ItemProperty -LiteralPath $saved -Name AutoUpgrade -PropertyType DWord -Value ([int] $entry.AutoUpgrade) -Force | Out-Null
                New-ItemProperty -LiteralPath $saved -Name AllowFallbackToUdp -PropertyType DWord -Value ([int] $entry.AllowFallbackToUdp) -Force | Out-Null
            }
        }
        if ($entry) {
            Set-DnsClientDohServerAddress -ServerAddress $server -AutoUpgrade $true -AllowFallbackToUdp $false | Out-Null
        } else {
            Add-DnsClientDohServerAddress -ServerAddress $server -DohTemplate $dohTemplate -AutoUpgrade $true -AllowFallbackToUdp $false | Out-Null
        }
    }
}
Clear-DnsClientCache
`

// disableScript runs elevated. Adapters still on Cloudflare go back to their
// saved servers, or to automatic DNS when they had none.
const disableScript = scriptPrelude + `
function Get-ValidServers([string] $value) {
    @($value -split '[,; ]+' | Where-Object { $_ -and $cloudflare -notcontains $_ } | ForEach-Object {
        $ip = $null
        if ([System.Net.IPAddress]::TryParse($_, [ref] $ip)) { $ip.ToString() }
    })
}
foreach ($adapter in @(Get-NetAdapter)) {
    $index = $adapter.ifIndex
    $saved = "$root\Adapters\" + [string] $adapter.InterfaceGuid
    if (@(Get-Servers $index | Where-Object { $cloudflare -contains $_ }).Count -eq 0) { continue }
    Set-DnsClientServerAddress -InterfaceIndex $index -ResetServerAddresses
    if (Test-Path -LiteralPath $saved) {
        $previous = Get-ItemProperty -LiteralPath $saved
        $servers = @(Get-ValidServers $previous.StaticV4) + @(Get-ValidServers $previous.StaticV6)
        if ($servers.Count -gt 0) { Set-DnsClientServerAddress -InterfaceIndex $index -ServerAddresses $servers }
    }
}
if ((Test-Path -LiteralPath "$root\DoH") -and (Get-Command Set-DnsClientDohServerAddress -ErrorAction SilentlyContinue)) {
    foreach ($server in $cloudflare) {
        $saved = "$root\DoH\" + $server.Replace(':', '_')
        if (-not (Test-Path -LiteralPath $saved)) { continue }
        $previous = Get-ItemProperty -LiteralPath $saved
        if ($previous.Existed -eq 1) {
            Set-DnsClientDohServerAddress -ServerAddress $server -AutoUpgrade ([bool] $previous.AutoUpgrade) -AllowFallbackToUdp ([bool] $previous.AllowFallbackToUdp) | Out-Null
        } elseif (Get-DnsClientDohServerAddress -ServerAddress $server -ErrorAction SilentlyContinue) {
            Remove-DnsClientDohServerAddress -ServerAddress $server | Out-Null
        }
    }
}
if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Recurse -Force }
$parent = Split-Path -Parent $root
if ((Test-Path -LiteralPath $parent) -and -not (Get-ChildItem -LiteralPath $parent)) { Remove-Item -LiteralPath $parent -Force }
Clear-DnsClientCache
`

// exitNoAdapter is the elevated scripts' exit code for "no connected adapter".
const exitNoAdapter = 3

// adapter is one connected network adapter as reported by PowerShell.
type adapter struct {
	Index   int      `json:"Index"`
	Alias   string   `json:"Alias"`
	Current []string `json:"Current"`
}

// Status reads whether the connected adapters use Cloudflare.
func Status(ctx context.Context) (State, error) {
	adapters, err := readAdapters(ctx)
	if err != nil {
		return StateUnavailable, err
	}
	return stateOf(adapters)
}

func stateOf(adapters []adapter) (State, error) {
	if len(adapters) == 0 {
		return StateUnavailable, ErrNoAdapter
	}
	for _, a := range adapters {
		if !usesCloudflare(a.Current) {
			return StateOff, nil
		}
	}
	return StateOn, nil
}

// Enable points every connected adapter at Cloudflare and turns on DNS over
// HTTPS for it. Windows asks the user for administrator approval.
func Enable(ctx context.Context) error {
	if err := runElevated(ctx, enableScript); err != nil {
		return err
	}
	if state, _ := Status(ctx); state != StateOn {
		return ErrNotApplied
	}
	return nil
}

// Disable restores the DNS settings saved by Enable. Windows asks the user
// for administrator approval.
func Disable(ctx context.Context) error {
	if err := runElevated(ctx, disableScript); err != nil {
		return err
	}
	adapters, err := readAdapters(ctx)
	if err != nil {
		return err
	}
	for _, a := range adapters {
		if usesCloudflare(a.Current) {
			return ErrNotApplied
		}
	}
	return nil
}

// powershellPath locates PowerShell through the Windows API rather than PATH
// or environment variables, which the user's profile can change.
func powershellPath() (string, error) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), nil
}

func encodeScript(script string) string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(raw[2*i:], u)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

const utf8BOM = string(rune(0xfeff))

func readAdapters(ctx context.Context) ([]adapter, error) {
	exe, err := powershellPath()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-NoProfile", "-NonInteractive", "-EncodedCommand", encodeScript(statusScript))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return nil, fmt.Errorf("read DNS settings: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("read DNS settings: %w", err)
	}
	var adapters []adapter
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(string(out), utf8BOM))), &adapters); err != nil {
		return nil, fmt.Errorf("read DNS settings: %w", err)
	}
	return adapters, nil
}

// shellExecuteInfo mirrors SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	swHide                = 0
)

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// runElevated runs one of the fixed scripts in an elevated, hidden
// PowerShell. Windows shows its UAC prompt first.
func runElevated(ctx context.Context, script string) error {
	exe, err := powershellPath()
	if err != nil {
		return err
	}
	wrapped := "try {\n" + script + "\nexit 0\n} catch { exit 1 }\n"
	params := "-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand " + encodeScript(wrapped)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil {
		defer windows.CoUninitialize()
	}

	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	args, err := windows.UTF16PtrFromString(params)
	if err != nil {
		return err
	}
	info := shellExecuteInfo{
		fMask:  seeMaskNoCloseProcess | seeMaskNoAsync,
		lpVerb: verb, lpFile: file, lpParameters: args, nShow: swHide,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if ok, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return ErrDeclined
		}
		return fmt.Errorf("start PowerShell as administrator: %w", callErr)
	}
	if info.hProcess == 0 {
		return errors.New("start PowerShell as administrator: no process was started")
	}
	defer windows.CloseHandle(info.hProcess)

	deadline := time.Now().Add(2 * time.Minute)
	for {
		event, err := windows.WaitForSingleObject(info.hProcess, 250)
		if err != nil {
			return fmt.Errorf("wait for PowerShell: %w", err)
		}
		if event == windows.WAIT_OBJECT_0 {
			break
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return errors.New("timed out waiting for Windows to change the DNS servers")
		}
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return fmt.Errorf("read PowerShell result: %w", err)
	}
	switch code {
	case 0:
		return nil
	case exitNoAdapter:
		return ErrNoAdapter
	default:
		return fmt.Errorf("Windows could not change the DNS servers (PowerShell exit code %d)", code)
	}
}
