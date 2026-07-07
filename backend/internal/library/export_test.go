// Package library exports internal symbols needed by the black-box test
// package. This file is compiled only during testing (it lives in package
// library, not package library_test, so it can reach unexported identifiers).
package library

import "time"

// SetScanTimeout overrides the package-level scanTimeout (the watchdog bound
// on StartScan's single-flight latch, production default 30m) and returns a
// restore func that puts the previous value back. Tests use this to shrink
// the timeout to a few milliseconds so the watchdog path fires
// deterministically within a short test timeout instead of waiting on the
// real production duration. Mirrors internal/sse/export_test.go's
// SetHeartbeatInterval seam.
func SetScanTimeout(d time.Duration) (restore func()) {
	prev := scanTimeout
	scanTimeout = d
	return func() { scanTimeout = prev }
}

// SetScanBlock installs (or clears, with nil) the scan-goroutine block seam and
// returns a restore func. When set, the scan goroutine waits on the channel (or
// scan-ctx cancel) before running — letting a test force the watchdog-timeout
// branch to win deterministically.
func SetScanBlock(ch chan struct{}) func() {
	prev := scanBlock
	scanBlock = ch
	return func() { scanBlock = prev }
}

// ProviderNameMatches exposes the unexported provider-name equality rule
// (case-insensitive, trimmed, blank-never-matches) to the black-box test
// package so it can be table-tested in isolation.
func ProviderNameMatches(diskProviderName, liveDisplayName string) bool {
	return providerNameMatches(diskProviderName, liveDisplayName)
}
