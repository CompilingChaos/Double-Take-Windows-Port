# DoubleTake

DoubleTake mirrors your desktop and audio to Apple TV, Mac, and other
compatible AirPlay receivers on your local network. It supports Linux Mint,
Ubuntu, and Windows. This repository builds on the
[original Doubletake project](https://github.com/omarroth/doubletake).

## Linux Mint and Ubuntu

The Linux x86-64 installer supports Ubuntu 24.04+ and Linux Mint 22+.
Open `doubletake-linux-amd64.deb` with your package installer, or install it
from a terminal:

```sh
sudo apt install ./doubletake-linux-amd64.deb
```

Installation adds **DoubleTake** to your application menu. Press the Windows
(Super) key, type **DoubleTake**, and press Enter. Run `doubletake` in a
terminal to use the CLI.

The portable `doubletake-linux-amd64.tar.gz` archive can be extracted and run
directly with `./doubletake-gui`. For the portable or source build, install the
runtime dependencies:

```sh
sudo apt install libwebkit2gtk-4.1-0 libxi6 gstreamer1.0-tools \
  gstreamer1.0-plugins-base gstreamer1.0-plugins-good \
  gstreamer1.0-plugins-bad gstreamer1.0-plugins-ugly pulseaudio-utils x11-xserver-utils
```

On Wayland, also install `gstreamer1.0-pipewire` and use the ScreenCast portal
for your desktop environment. Ubuntu GNOME uses `xdg-desktop-portal-gnome`.
Choose the screen to share when the desktop prompts you. On X11, DoubleTake
captures the primary screen. Audio capture uses the monitor of your default
PulseAudio or PipeWire-Pulse output, and local playback continues while streaming.

To install from source for your user, install Go 1.25+ and run:

```sh
sudo apt install build-essential pkg-config libwebkit2gtk-4.1-dev libxi-dev desktop-file-utils
make install PREFIX="$HOME/.local" DATADIR="${XDG_DATA_HOME:-$HOME/.local/share}"
```

This also adds the application menu entry. Use `make uninstall` with the same
`PREFIX` and `DATADIR` to remove the source installation.

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

Click **Freeze** or **Unfreeze**, then press **Enter** to apply it. On Windows
and Linux X11, Enter works while another app has focus. On Wayland, press
Enter in the DoubleTake window. Press Escape in DoubleTake to cancel.

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
