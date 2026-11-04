package airplay

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestScreenCastRequestReceivesSignalBeforeMethodReturns(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon is unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bus := exec.CommandContext(ctx, "dbus-daemon", "--session", "--nofork", "--nopidfile", "--print-address=1")
	stdout, err := bus.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = bus.Wait()
	}()
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read private bus address: %v", err)
	}
	connect := func() *dbus.Conn {
		t.Helper()
		conn, err := dbus.Connect(strings.TrimSpace(address), dbus.WithContext(ctx))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}

	service := connect()
	client := connect()
	const busName = "org.freedesktop.portal.Desktop"
	const objectPath = "/org/freedesktop/portal/desktop"
	if reply, err := service.RequestName(busName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own ScreenCast service name: %v (%v)", reply, err)
	}
	requestPath := dbus.ObjectPath(objectPath + "/request/test/immediate")
	mock := &immediateScreenCastPortal{conn: service, path: requestPath}
	if err := service.Export(mock, objectPath, "org.freedesktop.portal.ScreenCast"); err != nil {
		t.Fatal(err)
	}

	result, err := callScreenCastRequest(ctx, client, client.Object(busName, objectPath), "CreateSession")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := result["marker"]; !ok || got.Value() != "immediate" {
		t.Fatalf("expected immediate portal response, got %v", result)
	}
}

type immediateScreenCastPortal struct {
	conn *dbus.Conn
	path dbus.ObjectPath
}

func (p *immediateScreenCastPortal) CreateSession() (dbus.ObjectPath, *dbus.Error) {
	result := map[string]dbus.Variant{"marker": dbus.MakeVariant("immediate")}
	if err := p.conn.Emit(p.path, "org.freedesktop.portal.Request.Response", uint32(0), result); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return p.path, nil
}
