package series

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/technobecet/tsundoku/internal/ent"
)

// SourceLister is the narrow port the health scan uses to learn which sources
// the engine currently has loaded, so a provider whose Suwayomi extension was
// uninstalled can be flagged HealthUnavailable. It is deliberately decoupled
// from the suwayomi package (the series domain stays free of engine types); the
// wiring supplies an adapter over suwayomi.Client.Sources.
//
// LoadedSourceIDs returns the set of loaded source ids keyed by their numeric
// Suwayomi id. The ok flag distinguishes "loaded successfully, this id is
// absent" (ok=true) from "could not determine the set" (ok=false) — the
// caller MUST treat ok=false (or err) as "flag nothing", never as "everything
// is missing", so a transient engine hiccup can never mark the whole library
// unavailable.
type SourceLister interface {
	LoadedSourceIDs(ctx context.Context) (set map[int64]struct{}, ok bool, err error)
}

// WithSourceLister attaches the loaded-source lister and returns the service,
// so production wires it fluently onto the constructor (mirrors
// WithCoverFetcher). It is OPTIONAL: a Service with no SourceLister attached
// (the default — every existing NewService / NewServiceWithStaleGrace call
// site, including every pre-existing test) never flags a source unavailable.
func (s *Service) WithSourceLister(l SourceLister) *Service {
	s.sourceLister = l
	return s
}

// loadedSources resolves the engine's currently-loaded source set ONCE for a
// whole health scan. It returns active=true only when the set was positively
// determined (a lister is attached, it reported ok, and no error) — the
// fail-safe contract: on a nil lister, an error, or ok=false it returns
// active=false so NO provider is flagged unavailable this scan. A load failure
// is logged once at WARN and swallowed (best-effort: a missing availability
// signal must never break the health read).
func (s *Service) loadedSources(ctx context.Context) (loaded map[int64]struct{}, active bool) {
	if s.sourceLister == nil {
		return nil, false
	}
	set, ok, err := s.sourceLister.LoadedSourceIDs(ctx)
	if err != nil {
		slog.Warn("series: could not load engine source set for availability check — no source flagged unavailable this scan", "error", err)
		return nil, false
	}
	if !ok {
		return nil, false
	}
	return set, true
}

// providerSourceUnavailable reports whether a LIVE provider's Suwayomi source
// is absent from the loaded set. A disk-origin provider (SuwayomiID == 0 — an
// unlinked group created by library import/reconcile, never a real engine
// source) is NEVER unavailable. An unparseable provider id can't prove absence,
// so it fails safe (not flagged). The numeric source id is the provider's
// Provider field (the raw Suwayomi source-ID identity key for a live row).
func providerSourceUnavailable(p *ent.SeriesProvider, loaded map[int64]struct{}) bool {
	if p.SuwayomiID == 0 {
		return false
	}
	id, err := strconv.ParseInt(p.Provider, 10, 64)
	if err != nil {
		return false
	}
	_, present := loaded[id]
	return !present
}
