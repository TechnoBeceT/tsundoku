package imports

import (
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"
)

// TestParseCoverURL pins the SSRF-hardening rule on the SourceCover ?url
// param: a legitimate source cover is always a public http(s) URL, so
// anything else (empty, non-http(s), scheme-relative, or an
// internal/loopback/private/link-local/metadata/CGNAT host) must be rejected
// with a 400 before the engine host is ever asked to fetch it. White-box
// (package imports, not imports_test) so this pure validator is verified
// with no Echo/DB/Docker dependency — mirrors the codebase's other
// no-DB unit tests (e.g. internal/handler/trackers/scoreformat_test.go).
func TestParseCoverURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{
			name:    "public https URL with query and fragment survives untouched",
			raw:     "https://theblank.net/storage/series/covers/x.webp?v=1#thumbnail",
			wantErr: false,
		},
		{name: "empty is rejected", raw: "", wantErr: true},
		{name: "localhost is rejected", raw: "http://localhost/x", wantErr: true},
		{name: "IPv4 loopback is rejected", raw: "http://127.0.0.1/x", wantErr: true},
		{name: "private RFC1918 host is rejected", raw: "http://10.0.1.5:9000/x", wantErr: true},
		{name: "private 192.168 host is rejected", raw: "http://192.168.1.1/x", wantErr: true},
		{name: "cloud metadata address is rejected", raw: "http://169.254.169.254/latest/meta-data/", wantErr: true},
		{name: "IPv6 loopback is rejected", raw: "http://[::1]/x", wantErr: true},
		{name: "non-http(s) scheme is rejected", raw: "ftp://example.com/x", wantErr: true},
		{name: "scheme-relative URL is rejected", raw: "//example.com/x", wantErr: true},
		{name: "unparseable garbage is rejected", raw: "not-a-url", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCoverURL(tt.raw)
			if tt.wantErr {
				assertCoverURLRejected(t, tt.raw, got, err)
				return
			}
			assertCoverURLAccepted(t, tt.raw, got, err)
		})
	}
}

// assertCoverURLRejected pins the SHAPE of a rejection, not merely the fact of
// one: the guard must fail with an *echo.HTTPError carrying 400, because that is
// what the central error middleware turns into a client-visible 400. A plain
// error would surface as a 500 and read as a server fault rather than as the
// SSRF guard refusing an unsafe URL.
func assertCoverURLRejected(t *testing.T, raw, got string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("parseCoverURL(%q) = %q, nil; want a 400 error", raw, got)
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("parseCoverURL(%q) error = %T, want *echo.HTTPError", raw, err)
	}
	if he.Code != http.StatusBadRequest {
		t.Errorf("parseCoverURL(%q) status = %d, want %d", raw, he.Code, http.StatusBadRequest)
	}
}

// assertCoverURLAccepted pins the accept path: an allowed URL is returned
// BYTE-FOR-BYTE, so query and fragment survive. The validator must not
// re-serialise the URL — a source cover often depends on its ?v= cache-buster.
func assertCoverURLAccepted(t *testing.T, raw, got string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("parseCoverURL(%q) unexpected error: %v", raw, err)
	}
	if got != raw {
		t.Errorf("parseCoverURL(%q) = %q, want the URL returned unchanged (query+fragment preserved)", raw, got)
	}
}

// TestIsPublicHTTPHost exercises the extracted SSRF host-literal guard
// directly (already-parsed Hostname() values, incl. IPv6-bracket-stripped
// and CGNAT — a range parseCoverURL's table above does not separately probe
// since it is not one of the finding's required reject cases, but the guard
// itself must cover it per the hardening spec).
func TestIsPublicHTTPHost(t *testing.T) {
	tests := []struct {
		name string
		host string
		want bool
	}{
		{"public hostname", "theblank.net", true},
		{"public IPv4", "203.0.113.10", true},
		{"empty host", "", false},
		{"localhost", "localhost", false},
		{"subdomain of localhost", "foo.localhost", false},
		{"localhost different case", "LOCALHOST", false},
		{"IPv4 loopback", "127.0.0.1", false},
		{"IPv6 loopback", "::1", false},
		{"RFC1918 10/8", "10.0.1.5", false},
		{"RFC1918 192.168/16", "192.168.1.1", false},
		{"RFC1918 172.16/12", "172.16.0.1", false},
		{"link-local unicast incl. cloud metadata", "169.254.169.254", false},
		{"link-local multicast", "224.0.0.251", false},
		{"unspecified IPv4", "0.0.0.0", false},
		{"unspecified IPv6", "::", false},
		{"IPv6 unique local (fc00::/7)", "fc00::1", false},
		{"IPv6 link-local (fe80::/10)", "fe80::1", false},
		{"CGNAT 100.64.0.0/10", "100.64.0.1", false},
		{"CGNAT range upper bound", "100.127.255.255", false},
		{"just outside CGNAT range is public", "100.128.0.1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPublicHTTPHost(tt.host); got != tt.want {
				t.Errorf("isPublicHTTPHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}
