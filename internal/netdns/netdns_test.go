package netdns

import "testing"

func TestUsesCloudflareLooksAtTheFirstIPv4Resolver(t *testing.T) {
	cases := []struct {
		servers []string
		want    bool
	}{
		{[]string{"1.1.1.1", "1.0.0.1"}, true},
		{[]string{"2606:4700:4700::1111", "1.0.0.1"}, true},
		{[]string{"192.168.1.1", "1.1.1.1"}, false},
		{[]string{"fe80::1", "192.168.1.1"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := usesCloudflare(c.servers); got != c.want {
			t.Errorf("usesCloudflare(%v) = %v, want %v", c.servers, got, c.want)
		}
	}
}
