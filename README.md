# DoubleTake

DoubleTake mirrors your desktop and audio to Apple TV, Mac, and other
compatible AirPlay receivers on your local network. It supports Linux Mint,
Ubuntu, and Windows. This repository builds on the
[original Doubletake project](https://github.com/omarroth/doubletake).

## Linux Mint and Ubuntu

The Linux x86-64 release is built on Ubuntu 24.04 for Ubuntu 24.04 and newer
and Linux Mint 22 and newer. Install the runtime packages:

```sh
sudo apt install libwebkit2gtk-4.1-0 gstreamer1.0-tools \
  gstreamer1.0-plugins-base gstreamer1.0-plugins-good \
  gstreamer1.0-plugins-bad gstreamer1.0-plugins-ugly pulseaudio-utils x11-xserver-utils
```

On Wayland, also install `gstreamer1.0-pipewire` and use the ScreenCast portal
for your desktop environment. Ubuntu GNOME uses `xdg-desktop-portal-gnome`.
Choose the screen to share when the desktop prompts you. On X11, DoubleTake
captures the primary screen. Audio capture uses the monitor of your default
PulseAudio or PipeWire-Pulse output, and local playback continues while streaming.

Extract `doubletake-linux-amd64.tar.gz` and run `./doubletake-gui` from your
desktop session. Run `./doubletake` to use receiver discovery from the terminal.

To build from source, install Go 1.25 or newer and run `make` to build the CLI,
control tool, and test receiver in `bin/`. To also build the desktop app:

```sh
sudo apt install build-essential pkg-config libwebkit2gtk-4.1-dev
make desktop
./bin/doubletake-gui
```

## Windows

Install Microsoft Edge WebView2 Runtime and FFmpeg. FFmpeg can be on `PATH` or
next to the DoubleTake executables. Install FFmpeg with WinGet:

```powershell
winget install --id Gyan.FFmpeg --exact
```

Extract the Windows release files to the same folder and open `doubletake.exe`.
Run `.\doubletake-cli.exe` to use receiver discovery from PowerShell.

To build from source, install Go 1.25 or newer and run `.\build.ps1` in
PowerShell. The app, CLI, control tool, and diagnostic receiver are built in
`bin\`.

On supported Windows versions, DoubleTake captures the system mix independently
of the default speakers. It mutes the local playback endpoint during audio
mirroring and restores its previous mute state when capture ends.

## Desktop controls

Choose a receiver and enter its pairing PIN or configured password if prompted.
DoubleTake saves pairing credentials for later connections. Receiver audio
starts muted. Use the speaker button or volume slider to enable it.

- **Linux** applies **Freeze** and **Unfreeze** when you click them.
- **Windows** arms **Freeze** or **Unfreeze** when you click it. Press Enter once
  from any app to apply it. Press Escape in DoubleTake to cancel an armed action.

## Command-line options

When discovery misses a receiver, connect directly using its address. The
default AirPlay port is `7000`. Use `-port` for a different port, `-no-audio` for
video only, and `-pair` to pair again. Run the CLI with `-help` for all options.

```sh
./doubletake -target 192.168.1.77 -no-audio
```

```powershell
.\doubletake-cli.exe -target 192.168.1.77 -no-audio
```

For a receiver with a fixed password, set `DOUBLETAKE_CODE` in the CLI's
environment before connecting.

## Test without an Apple TV

On Linux, run the bundled diagnostic receiver and the CLI in separate terminals:

```sh
./doubletake-test-receiver -profile appletv3 -listen 127.0.0.1:7000
./doubletake -target 127.0.0.1 -port 7000 -test -no-audio
```

On Windows, build from source and run these commands in separate PowerShell
windows from the repository root:

```powershell
.\bin\doubletake-test-receiver.exe -profile appletv3 -listen 127.0.0.1:7000
.\bin\doubletake-cli.exe -target 127.0.0.1 -port 7000 -test -no-audio
```

The diagnostic receiver reports protocol activity and packet counts.
