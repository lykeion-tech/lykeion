package handler

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIPTrustsForwardedOnlyFromTrustedProxies(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("172.30.0.0/24")}
	for _, c := range []struct {
		name    string
		trusted []netip.Prefix
		remote  string
		xff     string
		realIP  string
		want    string
	}{
		{"no trusted proxies ignores headers", nil, "172.30.0.5:4000", "203.0.113.9", "", "172.30.0.5"},
		{"untrusted peer cannot spoof", proxies, "198.51.100.7:4000", "203.0.113.9", "203.0.113.10", "198.51.100.7"},
		{"trusted proxy forwards the first hop", proxies, "172.30.0.5:4000", "203.0.113.9, 10.0.0.1", "", "203.0.113.9"},
		{"trusted proxy falls back to X-Real-IP", proxies, "172.30.0.5:4000", "", "203.0.113.10", "203.0.113.10"},
		{"garbage forwarded value keeps the peer", proxies, "172.30.0.5:4000", "not-an-ip", "", "172.30.0.5"},
		{"trusted proxy without headers keeps the peer", proxies, "172.30.0.5:4000", "", "", "172.30.0.5"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/auth/send-code", nil)
			r.RemoteAddr = c.remote
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.realIP != "" {
				r.Header.Set("X-Real-IP", c.realIP)
			}
			if got := (&H{TrustedProxies: c.trusted}).clientIP(r); got != c.want {
				t.Fatalf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got := ParseTrustedProxies(" 172.30.0.0/24, bogus ,10.0.0.1/32,")
	if len(got) != 2 || got[0].String() != "172.30.0.0/24" || got[1].String() != "10.0.0.1/32" {
		t.Fatalf("ParseTrustedProxies = %v", got)
	}
}
