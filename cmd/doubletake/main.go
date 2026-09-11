package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"doubletake/internal/airplay"
	"doubletake/internal/daemon"
)

var errSelectionCancelled = errors.New("selection cancelled")

// parsePortRange parses a "min-max" string into inclusive port bounds.
// An empty string returns (0, 0, nil) meaning "let the OS pick".
func parsePortRange(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected MIN-MAX, got %q", s)
	}
	lo, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("min: %w", err)
	}
	hi, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("max: %w", err)
	}
	if lo < 1 || hi > 65535 || lo > hi {
		return 0, 0, fmt.Errorf("range %d-%d out of bounds (1-65535, min<=max)", lo, hi)
	}
	if hi-lo+1 < 3 {
		return 0, 0, fmt.Errorf("range %d-%d too small; need at least 3 consecutive UDP ports", lo, hi)
	}
	return lo, hi, nil
}

func main() {
	defer pauseForStandaloneConsole()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	target := flag.String("target", "", "AirPlay receiver IP address or hostname (skip discovery)")
	port := flag.Int("port", 7000, "AirPlay port")
	var credentialFlag string
	flag.StringVar(&credentialFlag, "code", "", "Pairing code or receiver password")
	flag.StringVar(&credentialFlag, "pin", "", "Alias for -code")
	credFile := flag.String("creds", airplay.DefaultCredentialsPath(), "Path to saved pairing credentials")
	credBackend := flag.String("cred-backend", "file", "Credential storage backend: file or keyring (system keyring)")
	forcePair := flag.Bool("pair", false, "Force new pairing even if credentials exist")
	fps := flag.Int("fps", 30, "Frames per second")
	bitrate := flag.Int("bitrate", 0, "Video bitrate in kbps (0 = auto, default tunes for resolution/FPS)")
	targetLatencyMs := flag.Int("target-latency-ms", 0, "Joint audio/video playout latency override in milliseconds (0 = automatic AirPlay policy)")
	hwaccelHelp := "Hardware acceleration: auto, nvenc, vaapi, none"
	if runtime.GOOS == "windows" {
		hwaccelHelp = "Hardware acceleration: auto, nvenc, none"
	}
	hwaccel := flag.String("hwaccel", "auto", hwaccelHelp)
	videoCodec := flag.String("video-codec", "auto", "Screen codec: auto, h264, or hevc (auto uses hardware HEVC for capable high-resolution receivers)")
	testMode := flag.Bool("test", false, "Use synthetic video instead of screen capture for debugging")
	noEncrypt := flag.Bool("no-encrypt", false, "Disable RTSP header encryption (debugging only; video frames are always encrypted)")
	directKey := flag.Bool("direct-key", false, "Use shk/shiv directly without SHA-512 derivation")
	noAudio := flag.Bool("no-audio", false, "Disable audio streaming")
	portRange := flag.String("port-range", "", "Local UDP port range for receiver timing/audio (e.g. \"60000-60010\"); empty = OS ephemeral. Needs at least 3 ports.")
	debug := flag.Bool("debug", false, "Enable verbose debug logging")
	daemonize := flag.Bool("daemonize", false, "Run as background daemon with a local control interface")
	socketPath := flag.String("socket", daemon.DefaultSocketPath(), "Daemon control endpoint (Unix socket path or Windows host:port)")
	noCursor := flag.Bool("no-cursor", false, "Don't show the mouse cursor in the captured video")
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		exitProgram(2)
	}
	if err := airplay.ValidateHWAccel(*hwaccel); err != nil {
		fatalf("invalid -hwaccel: %v", err)
	}
	if err := airplay.ValidateVideoCodec(*videoCodec); err != nil {
		fatalf("invalid -video-codec: %v", err)
	}
	portMin, portMax, err := parsePortRange(*portRange)
	if err != nil {
		fatalf("invalid -port-range: %v", err)
	}
	if err := airplay.CheckFFmpeg(); err != nil {
		fatalf("%v", err)
	}

	airplay.SetTargetLatency(time.Duration(*targetLatencyMs) * time.Millisecond)

	airplay.SetDebugMode(*debug)
	credential := credentialFlag
	if env := os.Getenv("DOUBLETAKE_CODE"); env != "" {
		credential = env
	}

	if *daemonize {
		runDaemon(*socketPath, *credFile, *credBackend, *fps, *bitrate, portMin, portMax, *hwaccel, airplay.VideoCodec(*videoCodec), !*noCursor, credential, *debug, *testMode, *noEncrypt, *directKey, *noAudio)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		cancel()
		// Give goroutines a moment to clean up, then force exit
		go func() {
			time.Sleep(3 * time.Second)
			log.Println("forced exit (timeout)")
			exitProgram(1)
		}()
		// Also force exit on second signal
		<-sigCh
		log.Println("forced exit")
		exitProgram(1)
	}()

	var addr string
	var advertisement *airplay.AirPlayDevice
	if *target != "" {
		addr = *target
	} else {
		device, err := selectDevice(ctx)
		if err != nil {
			if errors.Is(err, errSelectionCancelled) {
				log.Println("selection cancelled")
				return
			}
			fatalf("discovery failed: %v", err)
		}
		addr = device.IP
		*port = device.Port
		advertisement = device
		fmt.Printf("selected: %s (%s:%d)\n", device.Name, device.IP, device.Port)
		if !*noAudio {
			*noAudio = promptNoAudio()
		}
	}

	newClient := func() *airplay.AirPlayClient {
		if advertisement != nil {
			device := *advertisement
			device.IP = addr
			device.Port = *port
			return airplay.NewAirPlayClientForDevice(device)
		}
		return airplay.NewAirPlayClient(addr, *port)
	}
	client := newClient()
	client.SetPassword(credential)
	if err := client.Connect(ctx); err != nil {
		fatalf("connect failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	info, err := client.GetInfo()
	if errors.Is(err, airplay.ErrCredentialsRequired) && credential == "" {
		credential = readCredential(bufio.NewReader(os.Stdin), "Enter the receiver's configured password: ")
		if credential == "" {
			fatalf("password cannot be empty")
		}
		client.SetPassword(credential)
		info, err = client.GetInfo()
	}
	if err != nil {
		fatalf("get info failed: %v", err)
	}
	log.Printf("connected to: %s (model: %s, initialVolume: %.1f)", info.Name, info.Model, info.InitialVolume)
	if credential == "" && info.RequiresPassword() {
		credential = readCredential(bufio.NewReader(os.Stdin), "Enter the receiver's configured password: ")
		if credential == "" {
			fatalf("password cannot be empty")
		}
		client.SetPassword(credential)
	}

	// Pairing flow:
	// 1. If --pair is forced, do full pair-setup and save credentials.
	// 2. If saved credentials exist, restore their negotiated protocol.
	// 3. Otherwise choose PIN/password or transient pairing from capabilities.
	needFullPair := *forcePair

	credStore, err := newCredentialStore(*credBackend, *credFile)
	if err != nil {
		fatalf("failed to load credentials: %v", err)
	}

	var savedCreds *airplay.SavedCredentials
	if !needFullPair {
		savedCreds = credStore.Lookup(info.DeviceID)
	}

	reconnect := func() {
		_ = client.Close()
		client = newClient()
		client.SetPassword(credential)
		if err := client.Connect(ctx); err != nil {
			fatalf("reconnect failed: %v", err)
		}
		var err error
		info, err = client.GetInfo()
		if err != nil {
			fatalf("get info after reconnect failed: %v", err)
		}
	}

	savePairingCredentials := func() {
		if client.PairKeys == nil {
			return
		}
		if err := credStore.SavePairing(info.DeviceID, client.PairingID,
			client.PairKeys.Ed25519Public, client.PairKeys.Ed25519Private,
			client.PairingProtocol()); err != nil {
			log.Printf("warning: failed to save credentials: %v", err)
		} else {
			log.Printf("credentials saved (%s)", *credBackend)
		}
	}

	pairWithCredential := func(value string, expectPIN bool) {
		if value == "" {
			value = credentialOrPrompt("", client, info, expectPIN)
		}
		credential = value
		client.SetPassword(credential)
		if err := client.Pair(ctx, value); err != nil {
			fatalf("pairing failed: %v", err)
		}
		savePairingCredentials()
	}

	if needFullPair {
		if forcePairUsesTransient(info) {
			if err := client.Pair(ctx, ""); err != nil {
				fatalf("transient pairing failed for password-protected receiver: %v", err)
			}
		} else {
			pairWithCredential(credential, !info.RequiresPassword())
		}
	} else if savedCreds != nil && savedCreds.HasPairingCredentials() {
		// Use saved credentials — pair-verify
		log.Printf("using saved credentials (%s)", *credBackend)
		verifyErr := restoreSavedPairing(client, savedCreds)
		if verifyErr == nil {
			verifyErr = client.PairVerify(ctx)
		}
		if verifyErr != nil {
			log.Printf("pair-verify with saved creds failed: %v", verifyErr)
			reconnect()
			if passwordRequiresPairing(info) {
				pairWithCredential("", false)
			} else if info.RequiredPairingCredential() == airplay.PairingCredentialPIN {
				pairWithCredential("", true)
			} else if err := client.Pair(ctx, ""); err != nil {
				log.Printf("transient pairing fallback failed: %v", err)
				reconnect()
				pairWithCredential("", false)
			}
		}
	} else {
		if passwordRequiresPairing(info) {
			pairWithCredential("", false)
		} else if info.RequiredPairingCredential() == airplay.PairingCredentialPIN {
			pairWithCredential("", true)
		} else if err := client.Pair(ctx, ""); err != nil {
			log.Printf("transient pairing failed: %v", err)
			reconnect()
			pairWithCredential("", false)
		}
	}
	log.Println("pairing complete")

	// FairPlay setup — establishes fp-setup state and ekey/eiv used for the
	// final encrypted mirror stream. Pair-verify and FairPlay are both needed
	// for Apple TV compatibility in the normal modern flow.
	if client.FpEkey == nil {
		if err := client.FairPlaySetup(ctx); err != nil {
			if !errors.Is(err, airplay.ErrFairPlayUnsupported) {
				fatalf("FairPlay setup failed: %v", err)
			}
			log.Printf("FairPlay SAP unsupported (%v); continuing with pair-verify DataStream setup", err)
		} else {
			log.Println("FairPlay setup complete")
		}
	}

	streamCfg := airplay.StreamConfig{
		FPS:                    *fps,
		Bitrate:                *bitrate,
		VideoCodec:             airplay.VideoCodec(*videoCodec),
		AutomaticHEVCAvailable: airplay.AutomaticHEVCAvailable(*hwaccel),
		NoEncrypt:              *noEncrypt,
		DirectKey:              *directKey,
		NoAudio:                *noAudio,
		PortMin:                portMin,
		PortMax:                portMax,
	}
	var captureWidth, captureHeight int
	var captureCodec airplay.VideoCodec
	prepareVideo := func(width, height int, codec airplay.VideoCodec) error {
		captureWidth, captureHeight = width, height
		captureCodec = codec
		return nil
	}
	session, err := client.SetupMirrorWithVideoCodecPreparation(ctx, streamCfg, prepareVideo)
	if errors.Is(err, airplay.ErrCredentialsRequired) && credential == "" {
		credential = readCredential(bufio.NewReader(os.Stdin), "Enter the code shown on the receiver, or its configured password: ")
		if credential == "" {
			fatalf("receiver code/password cannot be empty")
		}
		client.SetPassword(credential)
		session, err = client.SetupMirrorWithVideoCodecPreparation(ctx, streamCfg, prepareVideo)
	}
	if err != nil {
		fatalf("mirror setup failed: %v", err)
	}
	defer session.Close()
	log.Printf("mirror session ready (data port: %d)", session.DataPort)

	var capture *airplay.ScreenCapture
	if *testMode {
		if *noAudio {
			log.Println("using synthetic video for debugging")
		} else {
			log.Println("using synthetic video and audio test tone for debugging")
		}
		var err error
		capture, err = airplay.StartTestCapture(ctx, airplay.CaptureConfig{
			FPS:        *fps,
			Bitrate:    *bitrate,
			HWAccel:    *hwaccel,
			VideoCodec: captureCodec,
			MaxWidth:   captureWidth,
			MaxHeight:  captureHeight,
			ShowCursor: !*noCursor,
		})
		if err != nil {
			fatalf("test capture failed: %v", err)
		}
	} else {
		captureCfg := airplay.CaptureConfig{
			FPS:        *fps,
			Bitrate:    *bitrate,
			HWAccel:    *hwaccel,
			VideoCodec: captureCodec,
			MaxWidth:   captureWidth,
			MaxHeight:  captureHeight,
			ShowCursor: !*noCursor,
		}
		var err error
		capture, err = airplay.StartCapture(ctx, captureCfg)
		if err != nil {
			fatalf("screen capture failed: %v", err)
		}
	}
	defer capture.Stop()
	go func() {
		<-ctx.Done()
		capture.Stop()
		session.Close()
	}()
	log.Println("screen capture started")

	// Start audio capture and streaming unless disabled.
	if !*noAudio && session.HasAudio() {
		audioCapture, err := airplay.StartAudioCapture(ctx, *testMode, session.AudioCodec())
		if err != nil {
			log.Printf("warning: audio capture failed: %v (continuing without audio)", err)
		} else {
			defer audioCapture.Stop()
			go func() {
				if err := session.StreamAudio(ctx, audioCapture, session.AudioStream()); err != nil && ctx.Err() == nil {
					log.Printf("audio streaming error: %v", err)
				}
			}()
			log.Println("audio capture started")
		}
	} else if !*noAudio {
		log.Println("audio disabled (receiver did not provide audio ports)")
	}

	if err := session.StreamFrames(ctx, capture, 0*time.Second); err != nil && ctx.Err() == nil {
		fatalf("streaming error: %v", err)
	}
	log.Println("stream ended")
}

func credentialOrPrompt(value string, client *airplay.AirPlayClient, info *airplay.ReceiverInfo, expectPIN bool) string {
	if value != "" {
		return value
	}
	displayErr := client.StartPINDisplay()
	if displayErr != nil {
		if airplay.IsHTTPStatusCode(displayErr, 403) {
			log.Printf("%s", airplay.PairingAccessDeniedHint(info))
		}
		log.Printf("warning: failed to trigger PIN display: %v", displayErr)
	}
	prompt := pairingCredentialPrompt(expectPIN, displayErr)
	credential := readCredential(bufio.NewReader(os.Stdin), prompt)
	if credential == "" {
		fatalf("pairing credential cannot be empty")
	}
	return credential
}

func pairingCredentialPrompt(expectPIN bool, displayErr error) string {
	if expectPIN && displayErr == nil {
		return "Enter the PIN shown on the receiver: "
	}
	return "Enter the receiver's configured password or pairing PIN: "
}

func passwordRequiresPairing(info *airplay.ReceiverInfo) bool {
	return info != nil && info.RequiredPairingCredential() == airplay.PairingCredentialPassword
}

func restoreSavedPairing(client *airplay.AirPlayClient, saved *airplay.SavedCredentials) error {
	return client.RestorePairingCredentials(saved)
}

func forcePairUsesTransient(info *airplay.ReceiverInfo) bool {
	return info != nil && info.RequiresPassword() &&
		info.RequiredPairingCredential() == airplay.PairingCredentialNone
}

func readCredential(reader *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fatalf("failed to read credential: %v", err)
	}
	return strings.TrimRight(line, "\r\n")
}

func promptNoAudio() bool {
	fmt.Print("Stream audio to this receiver? [Y/n]: ")
	var input string
	if _, err := fmt.Scanln(&input); err != nil {
		return false
	}
	input = strings.TrimSpace(input)
	return strings.EqualFold(input, "n") || strings.EqualFold(input, "no")
}

func selectDevice(ctx context.Context) (*airplay.AirPlayDevice, error) {
	fmt.Println("searching for AirPlay receivers...")
	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	devices, err := airplay.DiscoverAirPlayDevices(discoverCtx)
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, noDevicesFoundError()
	}

	sort.Slice(devices, func(i, j int) bool {
		genI := appleTVModelGeneration(devices[i].Model)
		genJ := appleTVModelGeneration(devices[j].Model)
		if genI != genJ {
			return genI > genJ
		}
		return compareIPs(devices[i].IP, devices[j].IP) < 0
	})

	fmt.Println("\navailable devices:")
	for i, d := range devices {
		fmt.Printf("  [%d] %s (%s) - %s\n", i+1, d.Name, d.Model, d.IP)
	}

	fmt.Print("\nselect device [1] (q to cancel): ")
	var input string
	fmt.Scanln(&input)
	input = strings.TrimSpace(input)
	if input == "" {
		return &devices[0], nil
	}
	if strings.EqualFold(input, "q") || strings.EqualFold(input, "quit") {
		return nil, errSelectionCancelled
	}

	idx, err := strconv.Atoi(input)
	if err != nil || idx < 1 || idx > len(devices) {
		return nil, fmt.Errorf("invalid selection")
	}
	return &devices[idx-1], nil
}

func noDevicesFoundError() error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("no AirPlay receivers found. On Windows this usually means mDNS/network discovery is blocked or the receiver is on another subnet. Set the active Wi-Fi/Ethernet network profile to Private, enable Network Discovery, make sure the receiver is on the same local network, or bypass discovery with -target <receiver-ip>")
	}
	return fmt.Errorf("no AirPlay receivers found")
}

func appleTVModelGeneration(model string) int {
	model = strings.TrimSpace(model)
	const prefix = "AppleTV"
	if !strings.HasPrefix(model, prefix) {
		return 0
	}
	rest := strings.TrimPrefix(model, prefix)
	major, _, _ := strings.Cut(rest, ",")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0
	}
	return n
}

// compareIPs compares two IP address strings numerically.
func compareIPs(a, b string) int {
	ipA := net.ParseIP(a)
	ipB := net.ParseIP(b)
	if ipA == nil && ipB == nil {
		return strings.Compare(a, b)
	}
	if ipA == nil {
		return 1
	}
	if ipB == nil {
		return -1
	}
	aBytes := ipA.To16()
	bBytes := ipB.To16()
	for i := range aBytes {
		if aBytes[i] < bBytes[i] {
			return -1
		}
		if aBytes[i] > bBytes[i] {
			return 1
		}
	}
	return 0
}

func runDaemon(socketPath, credFile, credBackend string, fps, bitrate, portMin, portMax int, hwaccel string, videoCodec airplay.VideoCodec, showCursor bool, code string, debug, testMode, noEncrypt, directKey, noAudio bool) {
	cfg := daemon.Config{
		SocketPath:  socketPath,
		CredFile:    credFile,
		CredBackend: credBackend,
		FPS:         fps,
		Bitrate:     bitrate,
		PortMin:     portMin,
		PortMax:     portMax,
		HWAccel:     hwaccel,
		VideoCodec:  videoCodec,
		ShowCursor:  showCursor,
		Code:        code,
		Debug:       debug,
		TestMode:    testMode,
		NoEncrypt:   noEncrypt,
		DirectKey:   directKey,
		NoAudio:     noAudio,
	}

	d, err := daemon.New(cfg)
	if err != nil {
		fatalf("[daemon] %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[daemon] shutting down...")
		cancel()
		d.Shutdown()
		<-sigCh
		log.Println("[daemon] forced exit")
		exitProgram(1)
	}()

	if err := d.Run(ctx); err != nil {
		fatalf("[daemon] %v", err)
	}
}

func newCredentialStore(backend, filePath string) (*airplay.CredentialStore, error) {
	switch backend {
	case "keyring":
		kb, err := airplay.NewKeyringBackend()
		if err != nil {
			return nil, err
		}
		return airplay.NewCredentialStoreWithBackend(kb), nil
	case "file":
		return airplay.NewCredentialStore(filePath)
	default:
		return nil, fmt.Errorf("unknown credential backend %q (use \"file\" or \"keyring\")", backend)
	}
}
