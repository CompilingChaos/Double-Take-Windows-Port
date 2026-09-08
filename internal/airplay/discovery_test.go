package airplay

import (
	"net"
	"testing"

	"github.com/grandcat/zeroconf"
)

func TestUnescapeDNSName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "escaped punctuation",
			in:   "Living\\ Room\\ \\(2\\)",
			want: "Living Room (2)",
		},
		{
			name: "utf8 apostrophe encoded as decimal bytes",
			in:   "Emily\\226\\128\\153s MacBook Pro",
			want: "Emily’s MacBook Pro",
		},
		{
			name: "simple ascii apostrophe remains literal",
			in:   "Emily\\'s MacBook Pro",
			want: "Emily's MacBook Pro",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unescapeDNSName(tt.in); got != tt.want {
				t.Fatalf("unescapeDNSName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSupportsFairPlaySAP(t *testing.T) {
	rokuFeatures := uint64(0x38bcf46007f8ad0)
	if (&ReceiverInfo{Features: rokuFeatures}).SupportsFairPlaySAP() {
		t.Fatalf("Roku feature mask unexpectedly advertises FPSAP")
	}
	if (&AirPlayDevice{Features: rokuFeatures}).SupportsFairPlaySAP() {
		t.Fatalf("Roku discovery feature mask unexpectedly advertises FPSAP")
	}

	withFairPlay := rokuFeatures | FeatureFPSAP25
	if !(&ReceiverInfo{Features: withFairPlay}).SupportsFairPlaySAP() {
		t.Fatalf("ReceiverInfo with FPSAP bit did not advertise FairPlay SAP")
	}
	if !(&AirPlayDevice{Features: withFairPlay}).SupportsFairPlaySAP() {
		t.Fatalf("AirPlayDevice with FPSAP bit did not advertise FairPlay SAP")
	}
}

func TestPreferAppleTVDevices(t *testing.T) {
	devices := []AirPlayDevice{
		{Name: "Mac", Model: "Mac14,2", Features: FeatureScreen},
		{Name: "TV", Model: "AppleTV14,1"},
	}
	got := preferAppleTVDevices(devices)
	if len(got) != 2 || got[0].Model != "AppleTV14,1" || got[1].Model != "Mac14,2" {
		t.Fatalf("preferAppleTVDevices() = %+v, want Apple TV first and Mac retained", got)
	}
}

func TestLooksLikeAppleTVReceiver(t *testing.T) {
	if !looksLikeAppleTVReceiver(ReceiverInfo{Model: "AppleTV14,1"}) {
		t.Fatal("AppleTV model was not recognized")
	}
	if looksLikeAppleTVReceiver(ReceiverInfo{Model: "MacBookAir10,1"}) {
		t.Fatal("Mac model was incorrectly treated as Apple TV")
	}
}

func TestLooksLikeSupportedAirPlayReceiver(t *testing.T) {
	if !looksLikeSupportedAirPlayReceiver(ReceiverInfo{Model: "AppleTV14,1"}) {
		t.Fatal("Apple TV was not accepted")
	}
	if !looksLikeSupportedAirPlayReceiver(ReceiverInfo{Model: "MacBookAir10,1", Features: FeatureScreen}) {
		t.Fatal("screen-capable Mac was not accepted")
	}
	if looksLikeSupportedAirPlayReceiver(ReceiverInfo{Model: "MacBookAir10,1"}) {
		t.Fatal("Mac without screen capability was accepted")
	}
}

func TestMergeAirPlayDevicesDeduplicatesAndFillsMetadata(t *testing.T) {
	mdns := []AirPlayDevice{{
		Name: "Living Room", IP: "192.168.1.10", Port: 7000,
	}}
	scan := []AirPlayDevice{{
		Model: "AppleTV14,1", IP: "192.168.1.10", Port: 7000,
		DeviceID: "AA:BB:CC:DD:EE:FF", Features: FeatureScreen,
	}}

	got := mergeAirPlayDevices(mdns, scan)
	if len(got) != 1 {
		t.Fatalf("mergeAirPlayDevices() returned %d devices, want 1", len(got))
	}
	if got[0].Name != "Living Room" || got[0].Model != "AppleTV14,1" {
		t.Fatalf("mergeAirPlayDevices() = %+v, want combined metadata", got[0])
	}
	if got[0].DeviceID != "AA:BB:CC:DD:EE:FF" || got[0].Features != FeatureScreen {
		t.Fatalf("mergeAirPlayDevices() lost receiver metadata: %+v", got[0])
	}
}

func TestMergeAirPlayDevicesKeepsDistinctAddresses(t *testing.T) {
	devices := mergeAirPlayDevices(
		[]AirPlayDevice{{Name: "One", IP: "192.168.1.10", Port: 7000}},
		[]AirPlayDevice{{Name: "Two", IP: "192.168.1.11", Port: 7000}},
	)
	if len(devices) != 2 {
		t.Fatalf("mergeAirPlayDevices() returned %d devices, want 2", len(devices))
	}
}

func TestParseFeaturesUsesLowThenHighWireOrder(t *testing.T) {
	if got, want := parseFeatures("0x89ABCDEF,0x01234567"), uint64(0x0123456789abcdef); got != want {
		t.Fatalf("parseFeatures() = 0x%x, want 0x%x", got, want)
	}
	if got := parseFeatures("not-a-feature-mask"); got != 0 {
		t.Fatalf("invalid parseFeatures() = 0x%x, want 0", got)
	}
}

func TestParseServiceEntryPreservesCapabilityAdvertisement(t *testing.T) {
	entry := zeroconf.NewServiceEntry("Office\\ MacBook", "_airplay._tcp", "local.")
	entry.Port = 7000
	entry.AddrIPv4 = []net.IP{net.ParseIP("192.168.1.124")}
	entry.Text = []string{
		"deviceid=46:7F:F0:39:E3:D8",
		"fex=1d9/St5/Fzw4oY7cDg",
		"features=0x4A7FDFD5,0x3C177FDE",
		"flags=0x18644",
		"model=MacBookAir10,1",
		"protovers=1.1",
		"pi=92a5af57-631f-4453-8eb2-d90aa0558dea",
		"psi=447FF039-E3D8-4828-A804-3A60F64DBFCA",
		"pk=9d6c6f7b96fd15faad5b840fca30d2399daae390a7855ce7cd85fff2c604af0e",
		"srcvers=980.77.2",
		"osvers=27.0",
		"vv=1",
	}

	device := parseServiceEntry(entry)
	if device == nil {
		t.Fatal("parseServiceEntry returned nil")
	}
	if device.Name != "Office MacBook" || device.IP != "192.168.1.124" || device.Port != 7000 {
		t.Fatalf("address fields = %+v", device)
	}
	if device.Model != "MacBookAir10,1" || device.DeviceID != "46:7F:F0:39:E3:D8" {
		t.Fatalf("identity fields = %+v", device)
	}
	if device.SourceVersion != "980.77.2" || device.ProtocolVersion != "1.1" || device.VV != 1 {
		t.Fatalf("version fields = %+v", device)
	}
	if device.Features != 0x3c177fde4a7fdfd5 || device.Flags != 0x18644 {
		t.Fatalf("legacy features/status = (0x%x, 0x%x)", device.Features, device.Flags)
	}
	if device.FEX != "1d9/St5/Fzw4oY7cDg" || device.FeaturesEx.Low64() != device.Features || !device.HasFeature(99) {
		t.Fatalf("extended features = %q/%x", device.FEX, []byte(device.FeaturesEx))
	}
	if device.RawTXT["osvers"] != "27.0" || device.RawTXT["fex"] != device.FEX {
		t.Fatalf("raw TXT = %#v", device.RawTXT)
	}
}
