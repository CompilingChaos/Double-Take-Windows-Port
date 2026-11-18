//go:build windows

package main

import (
	"context"
	"os"
	"strings"
	"time"

	"doubletake/internal/install"
	"doubletake/internal/netdns"
	"golang.org/x/sys/windows"
)

// idYes is the MessageBox result for the Yes button.
const idYes = 6

// handleInstall runs before the window opens. It returns true when this
// process has finished: it started the installed copy, or it uninstalled.
func handleInstall() bool {
	args := os.Args[1:]
	for _, arg := range args {
		if strings.EqualFold(arg, install.UninstallFlag) {
			runUninstall()
			return true
		}
	}
	launched, err := install.Ensure(version, args)
	if err != nil {
		showMessage(windows.MB_OK|windows.MB_ICONWARNING, tr(
			"DoubleTake could not be installed:\n\n"+err.Error()+"\n\nIt will run from its current folder this time.",
			"DoubleTake konnte nicht installiert werden:\n\n"+err.Error()+"\n\nEs läuft dieses Mal aus seinem aktuellen Ordner."))
		return false
	}
	return launched
}

func runUninstall() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dnsOn := false
	if state, _ := netdns.Status(ctx); state == netdns.StateOn {
		dnsOn = true
	}
	question := tr(
		"Remove DoubleTake from this computer?\n\nYour saved receiver pairings are kept.",
		"DoubleTake von diesem Computer entfernen?\n\nDeine gespeicherten Kopplungen bleiben erhalten.")
	if dnsOn {
		question += tr(
			"\n\nCloudflare DNS is still on. DoubleTake will restore your previous DNS servers first; Windows will ask for administrator permission. This DNS feature is experimental and not guaranteed to be secure.",
			"\n\nCloudflare-DNS ist noch aktiv. DoubleTake stellt zuerst deine vorherigen DNS-Server wieder her; Windows fragt nach Administratorrechten. Diese DNS-Funktion ist experimentell und nicht garantiert sicher.")
	}
	if showMessage(windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_DEFBUTTON2, question) != idYes {
		return
	}
	if dnsOn {
		if err := netdns.Disable(ctx); err != nil {
			// Keep the app so the user can still switch DNS back.
			showMessage(windows.MB_OK|windows.MB_ICONERROR, tr(
				"DoubleTake was not removed, because Cloudflare DNS could not be turned off:\n\n"+dnsErrorMessage(err),
				"DoubleTake wurde nicht entfernt, weil Cloudflare-DNS nicht ausgeschaltet werden konnte:\n\n"+dnsErrorMessage(err)))
			return
		}
	}
	if err := install.Uninstall(); err != nil {
		showMessage(windows.MB_OK|windows.MB_ICONWARNING, tr(
			"DoubleTake was removed, but some files could not be deleted:\n\n"+err.Error(),
			"DoubleTake wurde entfernt, aber einige Dateien konnten nicht gelöscht werden:\n\n"+err.Error()))
		return
	}
	showMessage(windows.MB_OK|windows.MB_ICONINFORMATION, tr("DoubleTake was removed.", "DoubleTake wurde entfernt."))
}

func tr(en, de string) string {
	if uiLocale() == "de" {
		return de
	}
	return en
}

func showMessage(style uint32, message string) int32 {
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("DoubleTake")
	result, _ := windows.MessageBox(0, text, title, style|windows.MB_SETFOREGROUND)
	return result
}
