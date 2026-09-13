# Double-Take Windows Port

This is a Windows port of the [original Doubletake project](https://github.com/omarroth/doubletake).

For Linux, use the original repository instead of this Windows port.

It mirrors the Windows desktop and audio to Apple TV, Mac, and other compatible
AirPlay receivers. It handles receiver discovery, PIN pairing, encrypted media
transport, video encoding, audio capture, saved credentials, and simultaneous
connections.

## Requirements

- Windows
- FFmpeg installed and available on `PATH`, or `ffmpeg.exe` in the same folder as `doubletake.exe`
- An AirPlay receiver on the same network

Install FFmpeg with WinGet:

```powershell
winget install --id Gyan.FFmpeg --exact
```

## Run

Extract the release files to a folder and double-click `doubletake.exe`.

Select a receiver from the list.

The first connection may ask for the PIN shown on the receiver. Credentials are saved automatically for later connections.

## If No Receiver Is Found

Connect directly using the receiver's IP address:

```powershell
.\doubletake.exe -target 192.168.1.77
```

The default AirPlay port is `7000`. Use `-port` if the receiver uses another port.

## Disable Audio

When selecting a discovered receiver, choose **No** when asked whether audio should be streamed.

To disable audio automatically:

```powershell
.\doubletake.exe -target 192.168.1.77 -no-audio
```

## Pair Again

To force a new pairing:

```powershell
.\doubletake.exe -target 192.168.1.77 -pair
```

For a receiver that uses a fixed password:

```powershell
$env:DOUBLETAKE_CODE = "your-password"
.\doubletake.exe -target 192.168.1.77
```

## Test Without an Apple TV

Start the included diagnostic receiver by double-clicking `doubletake-test-receiver.exe`, or run it with these options:

```powershell
.\doubletake-test-receiver.exe -profile appletv3 -name "Test Apple TV" -listen 127.0.0.1:7000
```

Then run:

```powershell
.\doubletake.exe -target 127.0.0.1 -port 7000 -test -no-audio
```

The test receiver reports protocol and packet statistics. It does not display video or play audio.

Use `doubletake.exe -help` for the complete option list.
