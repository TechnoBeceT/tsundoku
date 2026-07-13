package metadatasvc

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
)

// maxRedirects bounds how many redirect hops the cover-fetch HTTP client
// follows. blockNonPublicControl below already re-validates the resolved
// address of EVERY hop (so a redirect into the internal network is blocked
// regardless of hop count) — this cap only guards against a pathological or
// looping redirect chain.
const maxRedirects = 5

// newSSRFSafeHTTPClient builds the PRODUCTION http.Client Service.fetchCoverBytes
// uses to fetch a cover image URL. auto-identify follows PROVIDER-supplied
// cover URLs automatically (see AutoIdentify in service.go), so this path is
// reachable from untrusted external data, not just an owner-typed URL, and
// must not be able to reach the host's own internal network.
//
// Callers needing to reach a local test double (httptest.Server listens on
// 127.0.0.1, which this guard deliberately blocks) use the WithHTTPClient
// Option instead — see NewService in service.go.
func newSSRFSafeHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: defaultHTTPTimeout,
		Control: blockNonPublicControl,
	}
	transport := &http.Transport{
		DialContext: dialer.DialContext,
	}
	return &http.Client{
		Timeout:   defaultHTTPTimeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("metadatasvc: stopped after %d redirects", maxRedirects)
			}
			return nil
		},
	}
}

// blockNonPublicControl is a net.Dialer.Control func: net/http invokes it for
// every dial the Transport performs (including each hop of a redirect chain)
// AFTER DNS resolution but BEFORE the socket connects, passing the concrete
// resolved address. Refusing here — rather than validating coverURL's
// hostname earlier, before resolution — is what makes the guard resilient to
// both a redirect to an internal host (each hop is a fresh dial, so it is
// re-checked) and DNS-rebinding/TOCTOU (a hostname that resolves to a public
// IP when first looked up and a private one moments later at connect time is
// still caught, because the check reads the address actually being dialed,
// never a cached earlier lookup).
func blockNonPublicControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("metadatasvc: cover fetch blocked: could not parse dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("metadatasvc: cover fetch blocked: %q is not a resolved IP", host)
	}
	if isBlockedIP(ip) {
		return fmt.Errorf("metadatasvc: cover fetch blocked: %s is not a public address", address)
	}
	return nil
}

// isBlockedIP reports whether ip is a non-publicly-routable address a cover
// fetch must never be allowed to reach: loopback (127.0.0.0/8, ::1), private
// (10/8, 172.16/12, 192.168/16, fc00::/7), link-local unicast or multicast
// (169.254.0.0/16, fe80::/10 — this range covers the cloud-metadata endpoint
// 169.254.169.254), unspecified (0.0.0.0, ::), or multicast.
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}
