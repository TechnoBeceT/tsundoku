package series

import (
	"context"
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
)

// sourceListerTimeout bounds the in-request engine call the health scan makes.
// GetSeries/LibraryHealth/UnhealthyCount invoke loadedSources synchronously, so
// a hung engine socket would otherwise stall the caller until its transport
// timeout. The availability signal is advisory and 2s is ample for a local
// engine, so the check can never dominate a detail-page load.
const sourceListerTimeout = 2 * time.Second

// SourceLister is the narrow port the health scan uses to learn which sources
// the engine currently has loaded, so a provider whose engine extension was
// uninstalled can be flagged HealthUnavailable. It is deliberately decoupled
// from the sourceengine package (the series domain stays free of engine types);
// the wiring supplies an adapter over sourceengine.Client.Sources.
//
// LoadedSourceIDs returns the set of loaded source ids keyed by their numeric
// source id. The ok flag distinguishes "loaded successfully, this id is absent"
// (ok=true) from "could not determine the set" (ok=false) — the caller MUST
// treat ok=false (or err) as "flag nothing", never as "everything is missing",
// so a transient engine hiccup can never mark the whole library unavailable.
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
// determined (a lister is attached, it reported ok, no error, and the set is
// NON-EMPTY) — the fail-safe contract: on a nil lister, an error, ok=false, OR
// an empty set it returns active=false so NO provider is flagged unavailable
// this scan. The engine call is bounded by sourceListerTimeout so a hung engine
// can't stall the caller (a timeout is just another error → fails safe). A load
// failure is logged once at WARN and swallowed (best-effort: a missing
// availability signal must never break the health read).
func (s *Service) loadedSources(ctx context.Context) (loaded map[int64]struct{}, active bool) {
	if s.sourceLister == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, sourceListerTimeout)
	defer cancel()
	set, ok, err := s.sourceLister.LoadedSourceIDs(ctx)
	if err != nil {
		slog.Warn("series: could not load engine source set for availability check — no source flagged unavailable this scan", "error", err)
		return nil, false
	}
	if !ok {
		return nil, false
	}
	// An empty-but-successful set is treated as engine-not-ready, NOT as "every
	// extension uninstalled". During an engine restart/reload window the endpoint
	// answers before its sources have loaded, momentarily returning an empty set;
	// flagging every live provider unavailable off that blip would spike
	// UnhealthyCount (an SSE) and could provoke a destructive "remove". The
	// genuine all-extensions-uninstalled state is vanishingly rare, so fail safe
	// — indistinguishable here from the nil/error/ok==false paths above.
	if len(set) == 0 {
		return nil, false
	}
	return set, true
}

// providerSourceUnavailable reports whether a LIVE provider's engine source is
// absent from the loaded set. A disk-origin provider (Provider is a display
// NAME, not a numeric source id — an unlinked group created by library
// import/reconcile, never a real engine source) is NEVER unavailable: the
// IsLinkedProvider guard excludes it. An unparseable/absent numeric id can't
// prove absence, so it fails safe (not flagged). The numeric source id is the
// provider's Provider field (the engine source-id identity key for a live row).
func providerSourceUnavailable(p *ent.SeriesProvider, loaded map[int64]struct{}) bool {
	if !IsLinkedProvider(p) {
		return false
	}
	// IsLinkedProvider true ⇒ ProviderSourceID parses, so ok is always true here.
	id, _ := ProviderSourceID(p)
	_, present := loaded[id]
	return !present
}
