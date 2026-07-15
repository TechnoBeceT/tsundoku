// Package warmup — Suwayomi source-picker exclusion rules.
//
// This file holds the warm-up job's OWN copy of the pre-P2 Suwayomi
// source-exclusion logic (Local source + owner-disabled + known-broken),
// previously shared with internal/imports via the exported
// imports.EnabledOnlineSources. P2 Suwayomi-removal (slice 3b) repointed
// internal/imports off suwayomi.Client entirely (its OWN, structurally
// different sourceengine-based exclusion now lives in imports/service.go), so
// the two packages' exclusion rules necessarily diverged — this is no longer
// "shared" logic, just relocated to its one remaining consumer. warmup stays
// on suwayomi.Client deliberately (out of scope for this slice; it is
// repointed in a later P2 slice), so this logic is UNCHANGED — a pure move.
package warmup

import (
	"context"
	"strings"

	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// isLocalSource reports whether src is Suwayomi's built-in Local source. The
// primary signal is the fixed id (suwayomi.LocalSourceID); the lang tag
// (case-insensitive "localsourcelang") is checked too as a defensive
// secondary signal, since it is the more stable identifier — if Suwayomi ever
// changes the id, matching on lang still catches it.
func isLocalSource(src suwayomi.Source) bool {
	return src.ID == suwayomi.LocalSourceID || strings.EqualFold(src.Lang, suwayomi.LocalSourceLang)
}

// isDisabledSource reports whether the owner has disabled src via the
// per-language enable/disable toggle (suwayomi.Source.Disabled, resolved from
// the source's isEnabled meta key — see suwayomi/source_meta.go).
func isDisabledSource(src suwayomi.Source) bool {
	return src.Disabled
}

// isBrokenSource reports whether src is a known-broken source Tsundoku must never
// touch — currently InfinityScans, whose captcha is broken (hitting it wastes
// requests + risks IP-blocks). Matched by NAME (case-insensitive). REMOVE this
// predicate (and its entry in excludedFromPicker) once the source's captcha works
// again.
func isBrokenSource(src suwayomi.Source) bool {
	return strings.EqualFold(src.Name, "InfinityScans")
}

// excludedFromPicker reports whether src must never be warmed: Suwayomi's
// built-in Local source, a source the owner has disabled, or a known-broken
// source (isBrokenSource).
func excludedFromPicker(src suwayomi.Source) bool {
	return isLocalSource(src) || isDisabledSource(src) || isBrokenSource(src)
}

// enabledOnlineSources returns every Suwayomi source eligible for the warm-up
// pass: all installed sources MINUS the built-in Local source and any source
// the owner has disabled (excludedFromPicker) — mirrors the exclusion rule the
// pre-P2 imports.Service.Search fan-out used to apply (see this file's package
// doc comment for why the two are no longer literally shared code).
func enabledOnlineSources(ctx context.Context, client suwayomi.Client) ([]suwayomi.Source, error) {
	all, err := client.Sources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]suwayomi.Source, 0, len(all))
	for _, src := range all {
		if excludedFromPicker(src) {
			continue
		}
		out = append(out, src)
	}
	return out, nil
}
