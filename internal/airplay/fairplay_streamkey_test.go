package airplay

import (
	"bytes"
	"crypto/sha512"
	"testing"
)

func TestDeriveStreamMasterKeyPreservesLegacyAppleTVPath(t *testing.T) {
	raw := bytes.Repeat([]byte{0xa5}, 16)
	secret := bytes.Repeat([]byte{0x5a}, 32)

	if got := deriveStreamMasterKey(raw, secret, false); !bytes.Equal(got, raw) {
		t.Fatalf("legacy key = %x, want raw key %x", got, raw)
	}

	h := sha512.New()
	h.Write(raw)
	h.Write(secret)
	wantModern := h.Sum(nil)[:16]
	if got := deriveStreamMasterKey(raw, secret, true); !bytes.Equal(got, wantModern) {
		t.Fatalf("modern key = %x, want %x", got, wantModern)
	}
}
