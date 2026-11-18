//go:build windows

package netdns

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestStateNeedsEveryAdapterOnCloudflare(t *testing.T) {
	on := adapter{Index: 1, Current: []string{"1.1.1.1", "1.0.0.1"}}
	off := adapter{Index: 2, Current: []string{"192.168.1.1"}}
	if state, _ := stateOf([]adapter{on}); state != StateOn {
		t.Errorf("one Cloudflare adapter: %v, want StateOn", state)
	}
	if state, _ := stateOf([]adapter{on, off}); state != StateOff {
		t.Errorf("mixed adapters: %v, want StateOff", state)
	}
	if state, err := stateOf(nil); state != StateUnavailable || err != ErrNoAdapter {
		t.Errorf("no adapters: %v %v, want StateUnavailable ErrNoAdapter", state, err)
	}
}

// The elevated scripts must be fixed text: no Go formatting is applied to
// them, so no value from outside can end up inside.
func TestElevatedScriptsAreConstantAndParse(t *testing.T) {
	exe, err := powershellPath()
	if err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{"status": statusScript, "enable": enableScript, "disable": disableScript} {
		if !strings.HasPrefix(strings.TrimSpace(script), "$ErrorActionPreference = 'Stop'") {
			t.Errorf("%s script does not start with the prelude", name)
		}
		// Parse only; nothing in the script runs.
		check := "$errors = $null; [void][System.Management.Automation.Language.Parser]::ParseInput($env:DT_SCRIPT, [ref]$null, [ref]$errors); $errors | ForEach-Object { $_.Message }; exit $errors.Count"
		cmd := exec.Command(exe, "-NoProfile", "-NonInteractive", "-Command", check)
		cmd.Env = append(os.Environ(), "DT_SCRIPT="+"try {\n"+script+"\nexit 0\n} catch { exit 1 }\n")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s script does not parse: %v\n%s", name, err, out)
		}
	}
}

func TestReadsThisComputersDNSSettings(t *testing.T) {
	if os.Getenv("DOUBLETAKE_DNS_READ_TEST") == "" {
		t.Skip("set DOUBLETAKE_DNS_READ_TEST=1 to read this computer's DNS settings (read-only)")
	}
	adapters, err := readAdapters(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := stateOf(adapters)
	t.Logf("%d adapter(s), state=%v err=%v", len(adapters), state, err)
}
