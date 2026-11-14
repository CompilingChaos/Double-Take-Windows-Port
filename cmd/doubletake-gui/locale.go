//go:build windows || (linux && cgo && desktop)

package main

import (
	"os"
	"strings"
)

func uiLocale() string {
	switch locale := strings.ToLower(strings.TrimSpace(os.Getenv("DOUBLETAKE_LANG"))); locale {
	case "en", "de":
		return locale
	default:
		return systemUILocale()
	}
}
