//go:build windows

// Package install makes doubletake.exe its own installer on Windows.
//
// Opening any doubletake.exe checks the per-user install folder
// (%LOCALAPPDATA%\Programs\DoubleTake). If that folder already holds a
// byte-for-byte identical doubletake.exe, the installed copy is started.
// Otherwise this copy installs itself there, adds Start menu and desktop
// shortcuts and an entry under Settings > Apps, and starts the installed
// copy. Nothing is added to startup, and no administrator rights are needed.
package install

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	appName      = "DoubleTake"
	exeName      = "doubletake.exe"
	shortcutName = "DoubleTake.lnk"
	uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\DoubleTake`
	// UninstallFlag makes doubletake.exe remove its installation.
	UninstallFlag = "--uninstall"
	// PortableFlag runs doubletake.exe where it is, without installing.
	PortableFlag = "--portable"
)

// companions are release files installed with doubletake.exe when they sit
// next to it.
var companions = []string{"doubletake-cli.exe", "doubletake-ctl.exe", "ffmpeg.exe"}

// Layout is where DoubleTake is installed and where its shortcuts go.
type Layout struct {
	Dir       string
	StartMenu string
	Desktop   string
}

// Target is the installed doubletake.exe.
func (l Layout) Target() string { return filepath.Join(l.Dir, exeName) }

// DefaultLayout is the per-user install location for the signed-in user.
func DefaultLayout() (Layout, error) {
	programs, err := windows.KnownFolderPath(windows.FOLDERID_UserProgramFiles, windows.KF_FLAG_CREATE)
	if err != nil {
		return Layout{}, fmt.Errorf("find the programs folder: %w", err)
	}
	startMenu, err := windows.KnownFolderPath(windows.FOLDERID_Programs, windows.KF_FLAG_CREATE)
	if err != nil {
		return Layout{}, fmt.Errorf("find the Start menu folder: %w", err)
	}
	desktop, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, 0)
	if err != nil {
		return Layout{}, fmt.Errorf("find the desktop folder: %w", err)
	}
	return Layout{Dir: filepath.Join(programs, appName), StartMenu: startMenu, Desktop: desktop}, nil
}

// Portable reports whether the user asked to run without installing.
func Portable(args []string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, PortableFlag) {
			return true
		}
	}
	return os.Getenv("DOUBLETAKE_PORTABLE") == "1"
}

// Ensure installs this doubletake.exe unless the identical file is already
// installed, then starts the installed copy with args. It returns true when
// the installed copy was started and this process should exit, and false
// when this process is the installed copy (or runs portable) and should
// carry on as the app.
func Ensure(version string, args []string) (bool, error) {
	if Portable(args) {
		return false, nil
	}
	self, err := os.Executable()
	if err != nil {
		return false, err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	layout, err := DefaultLayout()
	if err != nil {
		return false, err
	}
	release, err := lockInstaller()
	if err != nil {
		return false, err
	}
	defer release()
	start := func(target string) error {
		cmd := exec.Command(target, args...)
		cmd.Dir = filepath.Dir(target)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start the installed DoubleTake: %w", err)
		}
		return cmd.Process.Release()
	}
	return ensure(self, version, layout, register, start)
}

func ensure(self, version string, l Layout, register func(Layout, string) error, start func(string) error) (bool, error) {
	target := l.Target()
	if samePath(self, target) {
		cleanupReplaced(l.Dir)
		return false, nil
	}
	if same, err := sameContent(self, target); err == nil && same {
		return true, start(target)
	}
	if err := installFiles(self, l); err != nil {
		return false, err
	}
	if err := createShortcuts(l); err != nil {
		return false, err
	}
	if err := register(l, version); err != nil {
		return false, err
	}
	return true, start(target)
}

// samePath compares two paths the way Windows does: without case and with
// short 8.3 names (such as GEORGS~1) expanded.
func samePath(a, b string) bool {
	return strings.EqualFold(longPath(a), longPath(b))
}

func longPath(path string) string {
	path = filepath.Clean(path)
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetLongPathName(name, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return path
	}
	return windows.UTF16ToString(buf[:n])
}

// sameContent compares two files by size and SHA-256. A missing second file
// is simply not the same.
func sameContent(a, b string) (bool, error) {
	infoA, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	infoB, err := os.Stat(b)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if infoA.Size() != infoB.Size() {
		return false, nil
	}
	hashA, err := fileHash(a)
	if err != nil {
		return false, err
	}
	hashB, err := fileHash(b)
	if err != nil {
		return false, err
	}
	return hashA == hashB, nil
}

func fileHash(path string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

func installFiles(self string, l Layout) error {
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", l.Dir, err)
	}
	if err := replaceFile(self, l.Target()); err != nil {
		return err
	}
	for _, name := range companions {
		source := filepath.Join(filepath.Dir(self), name)
		if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := replaceFile(source, filepath.Join(l.Dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// replaceFile copies source over destination. Windows lets a running program
// be renamed but not overwritten, so an older copy that is still open moves
// aside to .old and is deleted the next time the installed app starts.
func replaceFile(source, destination string) error {
	incoming := destination + ".new"
	if err := copyFile(source, incoming); err != nil {
		_ = os.Remove(incoming)
		return fmt.Errorf("copy %s: %w", filepath.Base(source), err)
	}
	old := destination + ".old"
	_ = os.Remove(old)
	hadOld := false
	if _, err := os.Stat(destination); err == nil {
		if err := os.Rename(destination, old); err != nil {
			_ = os.Remove(incoming)
			return fmt.Errorf("replace %s: %w", filepath.Base(destination), err)
		}
		hadOld = true
	}
	if err := os.Rename(incoming, destination); err != nil {
		if hadOld {
			_ = os.Rename(old, destination)
		}
		_ = os.Remove(incoming)
		return fmt.Errorf("replace %s: %w", filepath.Base(destination), err)
	}
	if hadOld {
		_ = os.Remove(old)
	}
	return nil
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// cleanupReplaced deletes copies moved aside by replaceFile.
func cleanupReplaced(dir string) {
	for _, name := range append([]string{exeName}, companions...) {
		_ = os.Remove(filepath.Join(dir, name+".old"))
		_ = os.Remove(filepath.Join(dir, name+".new"))
	}
}

func createShortcuts(l Layout) error {
	description := "Mirror your screen and audio to Apple TV"
	for _, folder := range []string{l.StartMenu, l.Desktop} {
		if err := createShortcut(filepath.Join(folder, shortcutName), l.Target(), l.Dir, description); err != nil {
			return fmt.Errorf("create shortcut in %s: %w", folder, err)
		}
	}
	return nil
}

// register adds DoubleTake to Settings > Apps for the current user.
func register(l Layout, version string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("register DoubleTake in Settings: %w", err)
	}
	defer key.Close()
	target := l.Target()
	var size uint32
	for _, name := range append([]string{exeName}, companions...) {
		if info, err := os.Stat(filepath.Join(l.Dir, name)); err == nil {
			size += uint32(info.Size() / 1024)
		}
	}
	for name, value := range map[string]string{
		"DisplayName":     appName,
		"DisplayVersion":  version,
		"DisplayIcon":     target + ",0",
		"InstallLocation": l.Dir,
		"UninstallString": `"` + target + `" ` + UninstallFlag,
		"InstallDate":     time.Now().Format("20060102"),
	} {
		if err := key.SetStringValue(name, value); err != nil {
			return fmt.Errorf("register DoubleTake in Settings: %w", err)
		}
	}
	for name, value := range map[string]uint32{"NoModify": 1, "NoRepair": 1, "EstimatedSize": size} {
		if err := key.SetDWordValue(name, value); err != nil {
			return fmt.Errorf("register DoubleTake in Settings: %w", err)
		}
	}
	return nil
}

// Uninstall removes the shortcuts, the Settings entry and the installed
// files. Only files DoubleTake installed are deleted; the folder itself goes
// only when that leaves it empty. Saved pairings in %APPDATA% are kept.
func Uninstall() error {
	l, err := DefaultLayout()
	if err != nil {
		return err
	}
	return uninstall(l, true)
}

func uninstall(l Layout, unregister bool) error {
	var problems []string
	for _, folder := range []string{l.StartMenu, l.Desktop} {
		if err := os.Remove(filepath.Join(folder, shortcutName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			problems = append(problems, err.Error())
		}
	}
	if unregister {
		if err := registry.DeleteKey(registry.CURRENT_USER, uninstallKey); err != nil && !errors.Is(err, registry.ErrNotExist) {
			problems = append(problems, err.Error())
		}
	}
	for _, name := range append([]string{exeName}, companions...) {
		for _, path := range []string{filepath.Join(l.Dir, name), filepath.Join(l.Dir, name+".old"), filepath.Join(l.Dir, name+".new")} {
			if err := removeOrMoveAside(path); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}
	if err := os.Remove(l.Dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		if entries, readErr := os.ReadDir(l.Dir); readErr != nil || len(entries) == 0 {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// removeOrMoveAside deletes a file. A running program cannot be deleted, so
// it is moved to the temp folder instead, where Windows cleans it up later.
func removeOrMoveAside(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	aside := filepath.Join(os.TempDir(), fmt.Sprintf("doubletake-removed-%d-%s", time.Now().UnixNano(), filepath.Base(path)))
	if renameErr := os.Rename(path, aside); renameErr != nil {
		return err
	}
	return nil
}

// lockInstaller keeps two DoubleTake windows opened at the same moment from
// installing over each other.
func lockInstaller() (func(), error) {
	name, _ := windows.UTF16PtrFromString(`Local\DoubleTakeInstaller`)
	mutex, err := windows.CreateMutex(nil, false, name)
	if mutex == 0 {
		return nil, fmt.Errorf("create installer lock: %w", err)
	}
	event, err := windows.WaitForSingleObject(mutex, 60_000)
	if err != nil || (event != windows.WAIT_OBJECT_0 && event != windows.WAIT_ABANDONED) {
		windows.CloseHandle(mutex)
		return nil, errors.New("another DoubleTake installer is still running")
	}
	return func() {
		windows.ReleaseMutex(mutex)
		windows.CloseHandle(mutex)
	}, nil
}

var (
	clsidShellLink = windows.GUID{Data1: 0x00021401, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidShellLinkW  = windows.GUID{Data1: 0x000214f9, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidPersistFile = windows.GUID{Data1: 0x0000010b, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}

	procCoCreateInstance = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")
)

// Method slots in the IShellLinkW and IPersistFile vtables.
const (
	slotQueryInterface      = 0
	slotRelease             = 2
	slotSetDescription      = 7
	slotSetWorkingDir       = 9
	slotSetIconLocation     = 17
	slotSetPath             = 20
	slotPersistFileSave     = 6
	clsctxInprocServer      = 1
	rpcEChangedMode         = 0x80010106
	sFalse              int = 1
)

type comObject struct{ vtable unsafe.Pointer }

func (o *comObject) call(slot int, args ...uintptr) uintptr {
	method := *(*uintptr)(unsafe.Add(o.vtable, uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(method, append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *comObject) release() { o.call(slotRelease) }

func checkHRESULT(what string, hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("%s: HRESULT 0x%08x", what, uint32(hr))
	}
	return nil
}

// createShortcut writes a .lnk file with IShellLink.
func createShortcut(path, target, workingDir, description string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); {
	case err == nil, errors.Is(err, syscall.Errno(sFalse)):
		defer windows.CoUninitialize()
	case errors.Is(err, syscall.Errno(rpcEChangedMode)):
	default:
		return fmt.Errorf("initialize COM: %w", err)
	}

	var link *comObject
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&link)))
	if err := checkHRESULT("create shell link", hr); err != nil {
		return err
	}
	defer link.release()

	for _, step := range []struct {
		what string
		slot int
		text string
		more []uintptr
	}{
		{"set shortcut target", slotSetPath, target, nil},
		{"set shortcut folder", slotSetWorkingDir, workingDir, nil},
		{"set shortcut description", slotSetDescription, description, nil},
		{"set shortcut icon", slotSetIconLocation, target, []uintptr{0}},
	} {
		text, err := windows.UTF16PtrFromString(step.text)
		if err != nil {
			return err
		}
		if err := checkHRESULT(step.what, link.call(step.slot, append([]uintptr{uintptr(unsafe.Pointer(text))}, step.more...)...)); err != nil {
			return err
		}
	}

	var file *comObject
	if err := checkHRESULT("open shortcut file", link.call(slotQueryInterface, uintptr(unsafe.Pointer(&iidPersistFile)), uintptr(unsafe.Pointer(&file)))); err != nil {
		return err
	}
	defer file.release()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return checkHRESULT("save shortcut", file.call(slotPersistFileSave, uintptr(unsafe.Pointer(name)), 1))
}
