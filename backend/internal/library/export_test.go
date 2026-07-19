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

// SetMatchBlock installs (or clears, with nil) the match-goroutine block seam
// (match_disk_provider_async.go) and returns a restore func. When set, the
// background match goroutine waits on the channel (or run-ctx cancel) before
// running the merge — letting the single-flight-guard test hold the first merge
// in flight deterministically while it fires a second start. Mirrors
// SetScanBlock.
func SetMatchBlock(ch chan struct{}) func() {
	prev := matchBlock
	matchBlock = ch
	return func() { matchBlock = prev }
}

// SetMatchTimeout overrides the package-level matchTimeout (the detached
// background-merge bound, production default 15m) and returns a restore func.
// Mirrors SetScanTimeout.
func SetMatchTimeout(d time.Duration) (restore func()) {
	prev := matchTimeout
	matchTimeout = d
	return func() { matchTimeout = prev }
}

// SetConsolidateBlock installs (or clears, with nil) the consolidation-goroutine
// block seam (consolidate_async.go) and returns a restore func. When set, the
// background consolidation goroutine waits on the channel (or run-ctx cancel)
// before running — letting the single-flight-guard test hold the first
// consolidation in flight deterministically while it fires a second start.
// Mirrors SetMatchBlock.
func SetConsolidateBlock(ch chan struct{}) func() {
	prev := consolidateBlock
	consolidateBlock = ch
	return func() { consolidateBlock = prev }
}

// SetConsolidatePerProviderTimeout overrides the per-provider slice of the
// detached consolidation bound (production default = matchTimeout) and returns a
// restore func. Mirrors SetMatchTimeout.
func SetConsolidatePerProviderTimeout(d time.Duration) (restore func()) {
	prev := consolidatePerProviderTimeout
	consolidatePerProviderTimeout = d
	return func() { consolidatePerProviderTimeout = prev }
}

// SafeMergeError exposes the unexported caller-safe merge-error mapper so the
// black-box test package can pin the error-hygiene contract (known sentinel →
// clean message, unmapped → generic "match failed", never the raw %w chain).
func SafeMergeError(err error) string {
	return safeMergeError(err)
}

// ProviderNameMatches exposes the unexported provider-name equality rule
// (case-insensitive, trimmed, blank-never-matches) to the black-box test
// package so it can be table-tested in isolation.
func ProviderNameMatches(diskProviderName, liveDisplayName string) bool {
	return providerNameMatches(diskProviderName, liveDisplayName)
}

// ClassifyAttachError exposes the unexported attach-error classifier so the
// black-box test package can pin the honest error taxonomy (cooled-down →
// ErrSourceUnavailable, else ErrSourceUpstream) in isolation — the branch the
// ungated owner-attach path never trips in practice, but must still map correctly.
func ClassifyAttachError(source string, err error) error {
	return classifyAttachError(source, err)
}
