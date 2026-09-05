package airplay

import "testing"

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
		{Name: "Mac", Model: "Mac14,2"},
		{Name: "TV", Model: "AppleTV14,1"},
	}
	got := preferAppleTVDevices(devices)
	if len(got) != 1 || got[0].Model != "AppleTV14,1" {
		t.Fatalf("preferAppleTVDevices() = %+v, want only Apple TV", got)
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
