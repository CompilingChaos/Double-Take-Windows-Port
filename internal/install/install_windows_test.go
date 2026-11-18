//go:build windows

package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// testLayout installs into a temporary folder; nothing touches the real
// Start menu, desktop or registry.
func testLayout(t *testing.T) (Layout, string) {
	t.Helper()
	root := t.TempDir()
	l := Layout{
		Dir:       filepath.Join(root, "Programs", appName),
		StartMenu: filepath.Join(root, "Start Menu"),
		Desktop:   filepath.Join(root, "Desktop"),
	}
	for _, dir := range []string{l.StartMenu, l.Desktop, filepath.Join(root, "download")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return l, filepath.Join(root, "download")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// shortcutTarget reads a .lnk file back through the Windows shell.
func shortcutTarget(t *testing.T, path string) string {
	t.Helper()
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-Command",
		"(New-Object -ComObject WScript.Shell).CreateShortcut($env:DT_LINK).TargetPath")
	cmd.Env = append(os.Environ(), "DT_LINK="+path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read shortcut %s: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

type recorder struct {
	registered []string
	started    []string
}

func (r *recorder) register(_ Layout, version string) error {
	r.registered = append(r.registered, version)
	return nil
}

func (r *recorder) start(target string) error {
	r.started = append(r.started, target)
	return nil
}

func TestFirstRunInstallsShortcutsAndStartsTheInstalledCopy(t *testing.T) {
	l, download := testLayout(t)
	self := filepath.Join(download, exeName)
	writeFile(t, self, "version 1")
	writeFile(t, filepath.Join(download, "doubletake-cli.exe"), "cli 1")
	var r recorder

	handled, err := ensure(self, "1.0.0", l, r.register, r.start)
	if err != nil || !handled {
		t.Fatalf("ensure = %v, %v; want installed and handled", handled, err)
	}
	if got := readFile(t, l.Target()); got != "version 1" {
		t.Fatalf("installed exe = %q", got)
	}
	if got := readFile(t, filepath.Join(l.Dir, "doubletake-cli.exe")); got != "cli 1" {
		t.Fatalf("installed CLI = %q", got)
	}
	if _, err := os.Stat(filepath.Join(l.Dir, "ffmpeg.exe")); !os.IsNotExist(err) {
		t.Fatal("a companion that was not next to the download was installed")
	}
	for _, folder := range []string{l.StartMenu, l.Desktop} {
		if got := shortcutTarget(t, filepath.Join(folder, shortcutName)); !samePath(got, l.Target()) {
			t.Fatalf("shortcut in %s points to %q, want %q", folder, got, l.Target())
		}
	}
	if len(r.registered) != 1 || r.registered[0] != "1.0.0" {
		t.Fatalf("registered = %v", r.registered)
	}
	if len(r.started) != 1 || !samePath(r.started[0], l.Target()) {
		t.Fatalf("started = %v, want the installed copy", r.started)
	}
}

func TestSameVersionStartsTheInstalledCopyWithoutReinstalling(t *testing.T) {
	l, download := testLayout(t)
	self := filepath.Join(download, exeName)
	writeFile(t, self, "version 1")
	var r recorder
	if _, err := ensure(self, "1.0.0", l, r.register, r.start); err != nil {
		t.Fatal(err)
	}
	// A shortcut the user deleted stays deleted when nothing new is installed.
	if err := os.Remove(filepath.Join(l.Desktop, shortcutName)); err != nil {
		t.Fatal(err)
	}

	handled, err := ensure(self, "1.0.0", l, r.register, r.start)
	if err != nil || !handled {
		t.Fatalf("ensure = %v, %v", handled, err)
	}
	if len(r.registered) != 1 {
		t.Fatalf("an identical copy was registered again: %v", r.registered)
	}
	if _, err := os.Stat(filepath.Join(l.Desktop, shortcutName)); !os.IsNotExist(err) {
		t.Fatal("an identical copy recreated the desktop shortcut")
	}
	if len(r.started) != 2 {
		t.Fatalf("started = %v", r.started)
	}
}

func TestDifferentBuildReplacesTheInstalledCopy(t *testing.T) {
	l, download := testLayout(t)
	self := filepath.Join(download, exeName)
	writeFile(t, self, "version 1")
	var r recorder
	if _, err := ensure(self, "1.0.0", l, r.register, r.start); err != nil {
		t.Fatal(err)
	}
	// Same length, different bytes: only the hash tells them apart.
	writeFile(t, self, "version 2")

	if _, err := ensure(self, "1.0.1", l, r.register, r.start); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, l.Target()); got != "version 2" {
		t.Fatalf("installed exe = %q, want the new build", got)
	}
	if len(r.registered) != 2 || r.registered[1] != "1.0.1" {
		t.Fatalf("registered = %v", r.registered)
	}
}

// TestHelperKeepsRunning is started as a separate process by
// TestReplacesARunningProgram; on its own it does nothing.
func TestHelperKeepsRunning(t *testing.T) {
	if os.Getenv("DOUBLETAKE_INSTALL_HELPER") != "1" {
		t.Skip("helper process only")
	}
	time.Sleep(20 * time.Second)
}

func TestReplacesARunningProgram(t *testing.T) {
	l, download := testLayout(t)
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Run a copy of this test binary from the install folder, the way an
	// older DoubleTake may still be open while a new one installs.
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFile(testBinary, l.Target()); err != nil {
		t.Fatal(err)
	}
	running := exec.Command(l.Target(), "-test.run=^TestHelperKeepsRunning$")
	running.Env = append(os.Environ(), "DOUBLETAKE_INSTALL_HELPER=1")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = running.Process.Kill()
		_ = running.Wait()
	}()
	time.Sleep(300 * time.Millisecond)
	if err := os.Remove(l.Target()); err == nil {
		t.Fatal("Windows allowed deleting a running program; the test is not exercising the in-use case")
	}

	self := filepath.Join(download, exeName)
	writeFile(t, self, "new")
	if err := replaceFile(self, l.Target()); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, l.Target()); got != "new" {
		t.Fatalf("installed exe = %q", got)
	}
	if _, err := os.Stat(l.Target() + ".old"); err != nil {
		t.Fatalf("the running copy was not moved aside: %v", err)
	}
}

func TestInstalledCopyRunsAsTheApp(t *testing.T) {
	l, _ := testLayout(t)
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, l.Target(), "installed")
	writeFile(t, l.Target()+".old", "left over")
	var r recorder

	handled, err := ensure(l.Target(), "1.0.0", l, r.register, r.start)
	if err != nil || handled {
		t.Fatalf("ensure = %v, %v; want the installed copy to keep running", handled, err)
	}
	if _, err := os.Stat(l.Target() + ".old"); !os.IsNotExist(err) {
		t.Fatal("the replaced copy was not cleaned up")
	}
	if len(r.started) != 0 {
		t.Fatalf("started = %v", r.started)
	}
}

func TestUninstallRemovesOnlyInstalledFiles(t *testing.T) {
	l, download := testLayout(t)
	self := filepath.Join(download, exeName)
	writeFile(t, self, "version 1")
	var r recorder
	if _, err := ensure(self, "1.0.0", l, r.register, r.start); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(l.Dir, "notes.txt"), "the user's own file")

	if err := uninstall(l, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{l.Target(), filepath.Join(l.StartMenu, shortcutName), filepath.Join(l.Desktop, shortcutName)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s still exists", path)
		}
	}
	if got := readFile(t, filepath.Join(l.Dir, "notes.txt")); got != "the user's own file" {
		t.Fatal("uninstall touched a file it did not install")
	}
}

func TestPortableSkipsInstalling(t *testing.T) {
	if !Portable([]string{"--PORTABLE"}) {
		t.Fatal("--portable was not recognised")
	}
	t.Setenv("DOUBLETAKE_PORTABLE", "1")
	if !Portable(nil) {
		t.Fatal("DOUBLETAKE_PORTABLE=1 was not recognised")
	}
}
