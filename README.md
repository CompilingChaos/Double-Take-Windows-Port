# Double-Take Windows Port

This is a Windows port of the [original Doubletake project](https://github.com/omarroth/doubletake).

For Linux, use the original repository instead of this Windows port.

It mirrors the Windows desktop and audio to Apple TV, Mac, and other compatible
AirPlay receivers. It handles receiver discovery, PIN pairing, encrypted media
transport, video encoding, audio capture, saved credentials, and simultaneous
connections.

## Requirements

- Windows
- Microsoft Edge WebView2 Runtime (normally already present on Windows 10 and 11)
- FFmpeg installed and available on `PATH`, or `ffmpeg.exe` in the same folder as `doubletake.exe`
- An AirPlay receiver on the same network

Install FFmpeg with WinGet:

```powershell
winget install --id Gyan.FFmpeg --exact
```

## Run

Extract the release files to a folder and double-click `doubletake.exe` to open
the desktop app. The app discovers nearby Apple TVs, lets you filter them by
name, connects with receiver audio muted by default, and provides disconnect,
freeze, and audio controls while streaming. It opens maximized.

Click **Freeze**, then press Enter once when you want to hold the current
picture. Click **Unfreeze**, then press Enter once to resume live video. Enter
works while another program is focused and still reaches that program normally.
Press Escape in DoubleTake to cancel an armed freeze or unfreeze.

For a video and audio session, use the speaker button to mute or unmute and the
slider to set that receiver's volume. Moving the slider above 0% unmutes it.
While DoubleTake mirrors audio, it mutes the local Windows playback endpoint
and restores its previous mute state when the last audio capture ends.
On supported Windows versions it captures the system mix through process
loopback, which keeps AirPlay audio independent of the speaker mute control.

Select a receiver from the list.

The first connection may ask for the PIN shown on the receiver. Credentials are saved automatically for later connections.

## If No Receiver Is Found

Connect directly using the receiver's IP address:

The command-line version is also included as `doubletake-cli.exe`:

```powershell
.\doubletake-cli.exe -target 192.168.1.77
```

The default AirPlay port is `7000`. Use `-port` if the receiver uses another port.

## Disable Audio

The desktop app connects with audio available but muted until you unmute it.
To disable audio entirely in the command-line version:

```powershell
.\doubletake-cli.exe -target 192.168.1.77 -no-audio
```

## Pair Again

To force a new pairing:

```powershell
.\doubletake-cli.exe -target 192.168.1.77 -pair
```

For a receiver that uses a fixed password:

```powershell
$env:DOUBLETAKE_CODE = "your-password"
.\doubletake-cli.exe -target 192.168.1.77
```

## Test Without an Apple TV

Start the included diagnostic receiver by double-clicking `doubletake-test-receiver.exe`, or run it with these options:

```powershell
.\doubletake-test-receiver.exe -profile appletv3 -name "Test Apple TV" -listen 127.0.0.1:7000
```

Then run:

```powershell
.\doubletake-cli.exe -target 127.0.0.1 -port 7000 -test -no-audio
```

The test receiver reports protocol and packet statistics. It does not display video or play audio.

Use `doubletake-cli.exe -help` for the complete command-line option list.
