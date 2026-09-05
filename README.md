# doubletake

AirPlay screen mirroring sender for Windows and Linux. Streams your desktop to an Apple TV using the AirPlay 2 mirroring protocol.

## Features

- Full AirPlay 2 mirroring protocol (RTSP/HTTP + encrypted video stream)
- FairPlay SAP authentication (snapshot-backed Go ARM64 execution)
- SRP-6a pairing with PIN and persistent credential storage
- Windows desktop capture (Direct3D/GDI), Wayland (PipeWire/xdg-desktop-portal), and X11 screen capture
- Hardware-accelerated H.264 encoding (Windows D3D11/NVENC, Linux NVENC/VA-API) with software fallback
- Windows WASAPI and Linux PulseAudio/PipeWire audio capture
- ChaCha20-Poly1305 stream encryption
- mDNS device discovery
	- Daemon mode with multi-target streaming control (`doubletake-ctl`)
- Configurable latency target (`-target-latency-ms`, default 100ms)
- KDE Plasma widget for Linux quick access (see [plasmoid/](plasmoid/))

## Requirements

- Go 1.23+
- Windows: FFmpeg for screen capture; native WASAPI loopback for audio
- Linux: GStreamer 1.0 with plugins-base, plugins-good, plugins-bad, plugins-ugly, and libav

### Windows

1. Install Go from <https://go.dev/dl/>.
2. Install FFmpeg and make sure `ffmpeg.exe` is on `PATH`.

```powershell
winget install --id Gyan.FFmpeg --exact
```

Windows screen capture uses FFmpeg `gdigrab` and outputs H.264 to doubletake. Windows audio uses native WASAPI loopback. GStreamer is not required on Windows.

### Ubuntu/Debian

```sh
sudo apt install libgstreamer1.0-dev libgstreamer-plugins-base1.0-dev \
  gstreamer1.0-plugins-base gstreamer1.0-plugins-good gstreamer1.0-plugins-bad \
  gstreamer1.0-plugins-ugly gstreamer1.0-libav
```

### Arch Linux

```sh
sudo pacman -S gstreamer gst-plugins-base gst-plugins-good gst-plugins-bad \
  gst-plugins-ugly gst-libav
```

## Build

### Windows

```powershell
.\build.ps1
```

This builds both binaries into `bin\`:

- `bin\doubletake.exe`
- `bin\doubletake-ctl.exe`

Run tests:

```powershell
.\build.ps1 -Test
```

### Linux/macOS/MSYS

```sh
make
```

This builds both binaries into `bin/`:

- `bin/doubletake`
- `bin/doubletake-ctl`

Run tests:

```sh
make test
```

## Install

On Windows, copy or add the `bin\` folder to your `PATH` after building.

On Unix-like systems, install binaries and man pages (default prefix: `/usr/local`):

```sh
sudo make install
```

Use a custom prefix if needed:

```sh
make install PREFIX=$HOME/.local
```

Uninstall:

```sh
sudo make uninstall
```

## Firewall

doubletake opens UDP ports (audio timing/control/data - 3 consecutive) and one TCP port (event channel) and advertises them to the Apple TV during SETUP. The Apple TV connects back to those ports. Until that reverse handshake completes, the receiver silently stalls and SETUP never returns.

By default the OS assigns ephemeral ports. Use `-port-range MIN-MAX` to confine them to a small window you can open in your firewall (needs at least 4 ports):

```sh
doubletake -target 192.168.1.77 -port-range 60000-60010
```

On Windows, allow `doubletake.exe` through Windows Defender Firewall, or open the chosen port range for inbound TCP and UDP from your local network.

On Linux with UFW:

```sh
sudo ufw allow from any proto udp to any port 60000:60010
sudo ufw allow from any proto tcp to any port 60000:60010
```

For nftables/firewalld, add equivalent rules allowing inbound UDP and TCP from the Apple TV's address on the chosen range.

## Discovery Troubleshooting

If Windows prints `discovery failed: no Apple TVs found`, discovery is failing before pairing or streaming starts. Check that the active Wi-Fi/Ethernet network is set to a Private network profile, Windows Network Discovery is enabled, and the AirPlay receiver is on the same local subnet as this PC. Guest Wi-Fi, hotel Wi-Fi, university networks, VPNs, and client-isolation settings often block mDNS discovery.

If you know the receiver's IP address, bypass discovery:

```powershell
.\bin\doubletake.exe -target 192.168.1.77
```

## Usage

```sh
# Discover Apple TVs on the network and stream
doubletake

# Disable audio for video-only mirroring
doubletake -no-audio

# Connect to a specific Apple TV
doubletake -target 192.168.1.77

# First-time pairing with PIN (saves credentials for reuse)
doubletake -target 192.168.1.77 -pair

# Use saved credentials
doubletake -target 192.168.1.77 -creds airplay-credentials.json

# Adjust stream settings (bitrate 0 = auto)
doubletake -target 192.168.1.77 -fps 30 -bitrate 0

# Force a lower bitrate on weaker Wi-Fi
doubletake -target 192.168.1.77 -bitrate 4500

# Set a target playout latency (default is 100ms)
doubletake -target 192.168.1.77 -target-latency-ms 100

# Hardware encoding
doubletake -target 192.168.1.77 -hwaccel nvenc   # NVIDIA
doubletake -target 192.168.1.77 -hwaccel vaapi   # Linux Intel/AMD

# Debug mode (verbose protocol logging)
doubletake -target 192.168.1.77 -debug

# Run daemon mode and control from a second shell
doubletake -daemonize
doubletake-ctl status
doubletake-ctl connect 192.168.1.77
doubletake-ctl connect 192.168.1.133
doubletake-ctl disconnect 192.168.1.77
doubletake-ctl disconnect
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-target` | | Apple TV IP (skip mDNS discovery) |
| `-port` | 7000 | AirPlay port |
| `-pin` | | 4-digit PIN for pairing |
| `-cred-backend` | `file` | Credential backend (`file` or `keyring`) |
| `-creds` | OS default | Credentials file path (`%APPDATA%\doubletake\credentials.json` on Windows, `~/.config/doubletake/credentials.json` on Linux) |
| `-pair` | false | Force new pairing |
| `-fps` | 30 | Frames per second |
| `-bitrate` | 0 | Video bitrate in kbps (`0` = auto) |
| `-target-latency-ms` | 100 | Target end-to-end latency in milliseconds (audio + video timing) |
| `-hwaccel` | auto | Hardware accel: `auto`, `nvenc`, `vaapi`, `none` |
| `-no-encrypt` | false | Disable RTSP header encryption (debugging only) |
| `-direct-key` | false | Use `shk`/`shiv` directly without SHA-512 derivation |
| `-no-audio` | false | Disable audio streaming |
| `-test` | false | Use synthetic video source |
| `-daemonize` | false | Run as background daemon with local control interface |
| `-socket` | OS default | Daemon control endpoint (`127.0.0.1:53531` on Windows, `$XDG_RUNTIME_DIR/doubletake.sock` on Linux) |
	| `-debug` | false | Verbose debug logging |

### Daemon Control (`doubletake-ctl`)

```sh
doubletake-ctl status
doubletake-ctl discover
doubletake-ctl devices
doubletake-ctl connect [target] [pin]
doubletake-ctl pin <4-digit-PIN>
doubletake-ctl disconnect [target]
doubletake-ctl mute [target]
doubletake-ctl unmute [target]
```

- `disconnect` without a target stops all active streams.
- `disconnect <target>` stops only that receiver.
- `mute`/`unmute` can operate globally or per target.

## Disclaimer

The majority of code for this project was written by LLMs. If you're in a production or security-sensitive environment and need to use AirPlay, review and test carefully before relying on this project.

Since I assume most of the code for this project was trained from [UxPlay](https://github.com/FDH2/UxPlay) and similar projects, this project is provided under the same license. Most of the reverse engineering work has already been done by many other people and this project would not be possible without them.

## License

This project is licensed under the [GNU General Public License v3.0](LICENSE). You are free to use, modify, and redistribute this software under the terms of the GPL-3.0. See the LICENSE file for full details.
