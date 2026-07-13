package library

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// AutoIdentifier is the narrow port the Phase-1 native metadata engine
// satisfies (metadatasvc.Service.AutoIdentify — see spec/metadata-engine-
// phase1 §4) — a background, best-effort pass that sets a freshly imported
// series' rich metadata + cover. Depending on this narrow interface rather
// than the whole metadatasvc.Service keeps this domain free of the metadata
// package (mirrors series.Service's CoverFetcher port over suwayomi.Client;
// deliberately duplicated rather than shared with internal/imports's own
// identical interface — see that package's AutoIdentifier doc comment for
// why each domain package defines its own narrow port).
type AutoIdentifier interface {
	// AutoIdentify runs the auto-identify pass for seriesID. "No confident
	// match anywhere" is an expected outcome (nil error) per the metadata
	// engine's own contract — see metadatasvc.Service.AutoIdentify's doc
	// comment.
	AutoIdentify(ctx context.Context, seriesID uuid.UUID) error
}

// WithAutoIdentifier attaches the metadata-engine auto-identify hook and
// returns the service, so production wires it fluently onto the constructor
// (mirrors series.Service.WithCoverFetcher). It is OPTIONAL: a Service with no
// AutoIdentifier attached (the default — every existing NewService call site)
// fires no background identify pass on Import; fireAutoIdentify below is then
// a silent no-op.
func (s *Service) WithAutoIdentifier(a AutoIdentifier) *Service {
	s.autoIdentifier = a
	return s
}

// fireAutoIdentify launches the DETACHED, best-effort auto-identify pass for a
// freshly imported (disk-only-adopted) series (spec/metadata-engine-phase1
// §4). Import's HTTP response must never wait on it — the goroutine runs on
// context.WithoutCancel(ctx) so it survives the request context being
// cancelled the instant the handler returns (mirrors
// internal/imports.Service.fireAutoIdentify).
//
// 🔴 NEVER-LINK-A-SOURCE INVARIANT: AutoIdentify (metadatasvc's own contract)
// writes metadata + cover ONLY — it must never create, modify, or delete a
// SeriesProvider or Chapter row. A library import is EXACTLY the scenario
// that invariant guards: the owner may be importing a series with zero
// attached Suwayomi sources (a pure disk migration), and auto-identify must
// never make that series look "sourced" when it is not.
//
// A nil autoIdentifier is a silent no-op (the common case — most callers
// never wire one). Any AutoIdentify failure is logged at WARN, never
// surfaced — this is best-effort by design.
func (s *Service) fireAutoIdentify(ctx context.Context, seriesID uuid.UUID) {
	if s.autoIdentifier == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		if err := s.autoIdentifier.AutoIdentify(detached, seriesID); err != nil {
			slog.WarnContext(detached, "library: auto-identify failed", "series_id", seriesID, "err", err)
		}
	}()
}
