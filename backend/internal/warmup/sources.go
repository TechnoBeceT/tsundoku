// Package warmup — engine-host source-picker exclusion rules.
//
// P2 Suwayomi-removal (slice 4) repointed this package onto
// internal/sourceengine.Client, mirroring internal/imports/service.go's
// ALREADY-RATIFIED exclusion rule (imports.excludedFromPicker) rather than
// keeping the old suwayomi.Client-era logic: the engine host models neither a
// built-in "Local" source nor a per-language enable/disable toggle, so
// isLocalSource + isDisabledSource are DROPPED (not reimplemented) — see
// imports/service.go's excludedFromPicker doc comment for the same
// KNOWN-GAP note. isBrokenSource is kept unchanged (name-based, no client
// dependency).
package warmup

import (
	"context"
	"strings"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// isBrokenSource reports whether src is a known-broken source Tsundoku must never
// touch — currently InfinityScans, whose captcha is broken (hitting it wastes
// requests + risks IP-blocks). Matched by NAME (case-insensitive). REMOVE this
// predicate (and its entry in excludedFromPicker) once the source's captcha works
// again.
func isBrokenSource(src sourceengine.Source) bool {
	return strings.EqualFold(src.Name, "InfinityScans")
}

// excludedFromPicker reports whether src must never be warmed: currently only a
// known-broken source (isBrokenSource) — mirrors internal/imports.excludedFromPicker.
func excludedFromPicker(src sourceengine.Source) bool {
	return isBrokenSource(src)
}

// enabledOnlineSources returns every engine-host source eligible for the
// warm-up pass: all loaded sources minus any known-broken one
// (excludedFromPicker) — mirrors the exclusion rule the pre-P2
// imports.Service.Search fan-out used to apply, now that both packages target
// internal/sourceengine.
func enabledOnlineSources(ctx context.Context, client sourceengine.Client) ([]sourceengine.Source, error) {
	all, err := client.Sources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]sourceengine.Source, 0, len(all))
	for _, src := range all {
		if excludedFromPicker(src) {
			continue
		}
		out = append(out, src)
	}
	return out, nil
}
