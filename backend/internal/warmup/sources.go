// Package warmup — engine-host source-picker exclusion rules.
//
// P2 Suwayomi-removal (slice 4) repointed this package onto
// internal/sourceengine.Client, mirroring internal/imports/service.go's
// exclusion rule (imports.excludedFromPicker) rather than keeping the old
// suwayomi.Client-era logic. The engine host has no built-in "Local" source, so
// isLocalSource stays dropped.
//
// GAP-146: the per-source owner-disable toggle (QCAT-513) IS honoured here now.
// It used to be dropped ("not reimplemented") because the engine host has no
// server-side enable/disable concept — but Tsundoku models the pause itself
// (internal/disabledsource), and warming a PAUSED source was the killer: the
// Popular call re-triggers exactly the anti-bot challenge the source is paused
// for and re-saturates the engine's RPC pool, so the pause could not stop the
// churn while warmup ignored it. The disabled set is now threaded through
// excludedFromPicker exactly as internal/imports does. isBrokenSource is kept
// unchanged (name-based, no client dependency).
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

// excludedFromPicker reports whether src must never be warmed: a known-broken
// source (isBrokenSource) OR a source the owner has PAUSED (its id is in the
// disabled set, GAP-146). Mirrors internal/imports.excludedFromPicker so the two
// exclusion rules can never drift; a nil/empty disabled set excludes only the
// broken sources.
func excludedFromPicker(src sourceengine.Source, disabled map[int64]bool) bool {
	return isBrokenSource(src) || disabled[src.ID]
}

// enabledOnlineSources returns every engine-host source eligible for the
// warm-up pass: all loaded sources minus any known-broken OR owner-PAUSED one
// (excludedFromPicker) — the SAME exclusion internal/imports applies to the
// Discover/Search picker. The disabled set is read ONCE per pass (disabledSet);
// a store read failure aborts the pass rather than warming a source the owner
// paused (fail-closed).
func (s *Service) enabledOnlineSources(ctx context.Context) ([]sourceengine.Source, error) {
	all, err := s.client.Sources(ctx)
	if err != nil {
		return nil, err
	}
	disabled, err := s.disabledSet(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]sourceengine.Source, 0, len(all))
	for _, src := range all {
		if excludedFromPicker(src, disabled) {
			continue
		}
		out = append(out, src)
	}
	return out, nil
}
