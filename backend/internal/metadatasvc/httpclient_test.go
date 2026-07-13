// White-box test file: package metadatasvc (not metadatasvc_test), because
// isBlockedIP is unexported. It is the pure classifier behind the SSRF
// guard's dial-time Control func (newSSRFSafeHTTPClient, httpclient.go) and
// is worth pinning in isolation, independent of any real network dial —
// every other metadatasvc test stays black-box (package metadatasvc_test).
package metadatasvc

import (
	"net"
	"testing"
)

// TestIsBlockedIP tables every address class the SSRF guard must classify:
// loopback, private (all three RFC1918 blocks + the IPv6 ULA range),
// link-local (including the cloud-metadata endpoint 169.254.169.254),
// unspecified, and two ordinary public IPs that must NOT be blocked.
func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		name    string
		ip      string
		blocked bool
	}{
		{"IPv4 loopback", "127.0.0.1", true},
		{"IPv6 loopback", "::1", true},
		{"IPv4 private 10/8", "10.0.0.1", true},
		{"IPv4 private 172.16/12", "172.16.0.1", true},
		{"IPv4 private 192.168/16", "192.168.1.1", true},
		{"cloud metadata endpoint (link-local)", "169.254.169.254", true},
		{"IPv6 link-local", "fe80::1", true},
		{"IPv4 unspecified", "0.0.0.0", true},
		{"IPv6 unspecified", "::", true},
		{"IPv4 multicast", "224.0.0.1", true},
		{"IPv6 unique local (private)", "fc00::1", true},
		{"public IPv4 (Google DNS)", "8.8.8.8", false},
		{"public IPv4 (Cloudflare DNS)", "1.1.1.1", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) = nil, want a parsed IP", tc.ip)
			}
			if got := isBlockedIP(ip); got != tc.blocked {
				t.Errorf("isBlockedIP(%s) = %v, want %v", tc.ip, got, tc.blocked)
			}
		})
	}
}
