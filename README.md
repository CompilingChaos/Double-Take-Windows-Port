# Double-Take Windows Port

This is a Windows port of the [original Doubletake project](https://github.com/omarroth/doubletake).

It mirrors the Windows desktop and audio to Apple TV, Mac, and other compatible
AirPlay receivers. It handles receiver discovery, PIN pairing, encrypted media
transport, video encoding, audio capture, saved credentials, and simultaneous
connections.

## Functionality

- Captures the Windows desktop with FFmpeg and audio through Windows WASAPI.
- Encodes H.264 with `libx264` or NVIDIA NVENC.
- Encodes HEVC Main10 with `libx265` or NVIDIA NVENC when the receiver and local
  hardware support it.
- Discovers AirPlay receivers through mDNS, including managed-network Bonjour
  gateways.
- Supports direct connections with a receiver IP address or hostname.
- Pairs with an onscreen PIN and saves credentials for later connections.
- Supports receiver passwords through `-code` or `DOUBLETAKE_CODE`.
- Streams to multiple receivers through the `doubletake-ctl` daemon interface.
- Supports optional audio disable, cursor hiding, bitrate, frame-rate, codec,
  latency, and UDP port-range controls.

## Requirements

- Windows with FFmpeg available on `PATH`, or `ffmpeg.exe` beside
  `doubletake.exe`.
- A receiver reachable from the same network, or a known receiver address.

Install FFmpeg with WinGet:

```powershell
winget install --id Gyan.FFmpeg --exact
```

## Build

```powershell
.\build.ps1
```

The executables are written to `bin\`:

- `doubletake.exe`
- `doubletake-ctl.exe`
- `doubletake-test-receiver.exe`

Run the test suite with:

```powershell
.\build.ps1 -Test
```

## Usage

Discover receivers and connect:

```powershell
doubletake
```

Connect directly when discovery is unavailable:

```powershell
doubletake -target 192.168.1.77
```

Pair with a receiver for the first time:

```powershell
doubletake -target 192.168.1.77 -pair
```

Useful options:

```powershell
doubletake -target 192.168.1.77 -no-audio
doubletake -target 192.168.1.77 -no-cursor
doubletake -target 192.168.1.77 -video-codec h264
doubletake -target 192.168.1.77 -video-codec hevc
doubletake -target 192.168.1.77 -fps 30 -bitrate 4500
doubletake -target 192.168.1.77 -port-range 60000-60010
```

Use `doubletake -help` for the complete option list.

## Pairing And Passwords

During pairing, enter the PIN shown on the receiver. Credentials are stored in:

```text
%APPDATA%\doubletake\credentials.json
```

For a receiver configured with a fixed password, use:

```powershell
$env:DOUBLETAKE_CODE = "your-password"
doubletake -target 192.168.1.77
```

The `-code` option can be used instead. The environment variable is preferred
because command-line arguments may be visible to other users.

## Discovery

Windows discovery uses mDNS and requests unicast responses so managed Bonjour
gateways can return AirPlay receivers across filtered multicast networks. It
also checks a small number of peers already known to Windows. It does not scan
the local subnet by default.

When the receiver address is known, `-target` is the most reliable option. An
active subnet fallback can be enabled on a trusted network with:

```powershell
$env:DOUBLETAKE_DISCOVERY_SUBNET_SCAN = "1"
doubletake
```

## Daemon Control

Run the daemon with a fixed local UDP port range:

```powershell
doubletake -daemonize -port-range 60000-60010
doubletake-ctl status
doubletake-ctl discover
doubletake-ctl devices
doubletake-ctl connect 192.168.1.77
doubletake-ctl disconnect 192.168.1.77
doubletake-ctl disconnect
```

Use `doubletake-ctl connect TARGET PIN-or-password` when a receiver requests a
PIN or password while multiple receivers are waiting.

## License

Licensed under the [GNU Lesser General Public License v3.0 or later](LICENSE).
