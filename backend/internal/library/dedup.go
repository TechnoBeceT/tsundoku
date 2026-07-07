package library

import (
	"strings"

	"github.com/technobecet/tsundoku/internal/ent"
)

// providerNameMatches reports whether a disk-origin provider's identity name
// and a live source's resolved display name refer to the same physical source.
// The comparison is case-insensitive and trims surrounding whitespace; two
// blank names never match (an empty display name is "unknown", not a wildcard),
// so a live source whose provider_name was never resolved is never merged.
func providerNameMatches(diskProviderName, liveDisplayName string) bool {
	a := strings.TrimSpace(diskProviderName)
	b := strings.TrimSpace(liveDisplayName)
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(a, b)
}

// matchingUnlinkedDiskProvider returns the unlinked disk-origin provider
// (suwayomi_id == 0) in providers whose identity name matches liveDisplayName
// (providerNameMatches) AND whose scanlator equals scanlator, or nil when none
// qualifies. This is how a disk import — which stores the display NAME in the
// provider field (suwayomi_id == 0) — is recognised as the same physical source
// a live ingest just attached, which stores the numeric source id in provider
// and the display name in provider_name (suwayomi_id != 0). Matching lets the
// two be folded into one row instead of drifting apart (see mergeDiskIntoLive).
func matchingUnlinkedDiskProvider(providers []*ent.SeriesProvider, liveDisplayName, scanlator string) *ent.SeriesProvider {
	for _, p := range providers {
		if p.SuwayomiID != 0 {
			continue
		}
		if p.Scanlator != scanlator {
			continue
		}
		if providerNameMatches(p.Provider, liveDisplayName) {
			return p
		}
	}
	return nil
}
