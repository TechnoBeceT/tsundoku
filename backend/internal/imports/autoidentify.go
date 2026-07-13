package imports

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// AutoIdentifier is the narrow port the Phase-1 native metadata engine
// satisfies (metadatasvc.Service.AutoIdentify — see spec/metadata-engine-
// phase1 §4) — a background, best-effort pass that sets a freshly adopted
// series' rich metadata + cover. Depending on this narrow interface rather
// than the whole metadatasvc.Service keeps this domain free of the metadata
// package (mirrors series.Service's CoverFetcher port over suwayomi.Client)
// and lets tests inject a fake that records whether/how it was called.
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
// AutoIdentifier attached (the default — every existing NewService /
// NewServiceWithCaches call site) fires no background identify pass on Adopt;
// fireAutoIdentify below is then a silent no-op.
func (s *Service) WithAutoIdentifier(a AutoIdentifier) *Service {
	s.autoIdentifier = a
	return s
}

// fireAutoIdentify launches the DETACHED, best-effort auto-identify pass for a
// freshly adopted series (spec/metadata-engine-phase1 §4). Adopt's HTTP
// response must never wait on it — the goroutine runs on
// context.WithoutCancel(ctx) (mirrors recordSamples' detached metrics write
// above) so it survives the request context being cancelled the instant the
// handler returns.
//
// 🔴 NEVER-LINK-A-SOURCE INVARIANT: AutoIdentify (metadatasvc's own contract)
// writes metadata + cover ONLY — it must never create, modify, or delete a
// SeriesProvider or Chapter row, so a background identify can never imply a
// download source and the library's "Needs source" signal stays accurate.
// This function does not (and must not) enforce that itself — it trusts the
// injected AutoIdentifier's own contract, exactly as fireAutoIdentify's
// counterpart in internal/library does.
//
// A nil autoIdentifier is a silent no-op (the common case — most callers
// never wire one). Any AutoIdentify failure is logged at WARN, never
// surfaced — this is best-effort by design: an owner who adopts a series
// sees it appear immediately regardless of whether metadata identification
// succeeds, fails, or finds no confident match.
func (s *Service) fireAutoIdentify(ctx context.Context, seriesID uuid.UUID) {
	if s.autoIdentifier == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		if err := s.autoIdentifier.AutoIdentify(detached, seriesID); err != nil {
			slog.WarnContext(detached, "imports: auto-identify failed", "series_id", seriesID, "err", err)
		}
	}()
}
