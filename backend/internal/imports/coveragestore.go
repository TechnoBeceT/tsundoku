package imports

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/sourcecoverage"
)

// Coverage snapshot lifecycle (GAP-140). A row is written `pending` when a job
// starts, so a second request can distinguish "already being computed" from
// "never computed" without a separate in-flight registry — and coverageNeedsCompute
// is what acts on the distinction, refusing to start a second walk for a pair
// whose row already claims one is running. See its doc comment for the full
// admission rule and its one known residual race.
const (
	coverageStatusPending = "pending"
	coverageStatusReady   = "ready"
	coverageStatusFailed  = "failed"
)

// CoverageSnapshot is one persisted per-scanlator breakdown plus the metadata
// the owner needs to judge it: what state it is in, when it was computed (the
// "as of" the UI renders), and why it failed if it did.
//
// UpdatedAt is the instant the ROW last changed, which is a different fact
// from ComputedAt (the as-of of the stored PAYLOAD, cleared whenever there is
// no payload). It is what Coverage's admission rules measure against: how long
// a `pending` row has been claiming a walk is running, and how long ago a
// `failed` one failed. ComputedAt cannot serve that purpose precisely because
// it is nil in both of those states.
type CoverageSnapshot struct {
	Payload    SourceBreakdownDTO
	Status     string
	ComputedAt *time.Time
	UpdatedAt  time.Time
	LastError  string
}

// loadCoverage returns the stored snapshot for (sourceID, mangaURL). A pair that
// was never computed is (zero, false, nil) — a MISS is not an error, so callers
// branch on ok rather than string-matching a not-found.
func (s *Service) loadCoverage(ctx context.Context, sourceID, mangaURL string) (CoverageSnapshot, bool, error) {
	row, err := s.db.SourceCoverage.Query().
		Where(sourcecoverage.SourceID(sourceID), sourcecoverage.MangaURL(mangaURL)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return CoverageSnapshot{}, false, nil
	}
	if err != nil {
		return CoverageSnapshot{}, false, fmt.Errorf("imports.loadCoverage: query %s %s: %w", sourceID, mangaURL, err)
	}

	snap := CoverageSnapshot{
		Status:     row.Status,
		ComputedAt: row.ComputedAt,
		UpdatedAt:  row.UpdatedAt,
		LastError:  row.LastError,
	}
	if row.Payload != "" {
		// A payload that no longer parses is treated as absent rather than
		// fatal: the snapshot is a cache, and a re-computation fixes it. It is
		// reported as `failed` carrying the ROW's own UpdatedAt, not a zero
		// one, so it is admitted for re-computation by exactly the same
		// cooldown rule as any other failure (see coverageNeedsCompute) — a
		// zero UpdatedAt would read as "failed at the epoch" and re-arm a walk
		// on every single request.
		if err := json.Unmarshal([]byte(row.Payload), &snap.Payload); err != nil {
			return CoverageSnapshot{
				Status:    coverageStatusFailed,
				UpdatedAt: row.UpdatedAt,
				LastError: "stored snapshot is unreadable",
			}, true, nil
		}
	}
	return snap, true, nil
}

// upsertCoverage is the ONE write path, so status/payload/error/computed_at can
// never drift apart across the three callers. The UNIQUE(source_id, manga_url)
// index makes the overwrite structural.
//
// The conflict branch spells out every column explicitly rather than calling
// UpdateNewValues(): computedAt is a Nillable field, so a nil never reaches
// Create()'s column list in the first place (SetNillableComputedAt is a no-op
// on nil) — UpdateNewValues() only re-asserts columns that WERE part of the
// insert, so it silently leaves computed_at at whatever the existing row
// already had. That let a failCoverage (or markCoveragePending) after a prior
// successful saveCoverage leave computed_at pointing at the OLD success's
// as-of, which is worse than no as-of at all next to a failure or an
// in-flight recomputation (GAP-140: confirmed live — a failed run showed a
// stale-but-plausible timestamp). computed_at is the as-of of the STORED
// PAYLOAD (the schema doc says as much: "Zero while pending"), so it is
// CLEARED explicitly whenever computedAt is nil, on every call, not only on
// the INSERT branch.
//
// updated_at is set explicitly for the SAME reason, and it is load-bearing
// rather than cosmetic: the schema's UpdateDefault(time.Now) fires on ent's
// ordinary Update builder, NOT on a conflict-do-update whose column list this
// closure spells out, so without the explicit set an existing row's
// updated_at would freeze at its first INSERT forever. Coverage's admission
// rules measure a `pending` row's age and a `failed` row's cooldown against
// that column, so a frozen one would let a fresh pending read as crash-stale
// (re-arming a duplicate walk on every request) and a fresh failure read as
// long past its cooldown — reinstating exactly the recompute loop those rules
// exist to stop.
func (s *Service) upsertCoverage(ctx context.Context, sourceID, mangaURL, status, payload, lastError string, computedAt *time.Time) error {
	err := s.db.SourceCoverage.Create().
		SetSourceID(sourceID).
		SetMangaURL(mangaURL).
		SetStatus(status).
		SetPayload(payload).
		SetLastError(lastError).
		SetNillableComputedAt(computedAt).
		OnConflictColumns(sourcecoverage.FieldSourceID, sourcecoverage.FieldMangaURL).
		Update(func(u *ent.SourceCoverageUpsert) {
			u.SetStatus(status)
			u.SetPayload(payload)
			u.SetLastError(lastError)
			u.SetUpdatedAt(time.Now().UTC())
			if computedAt != nil {
				u.SetComputedAt(*computedAt)
			} else {
				u.ClearComputedAt()
			}
		}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("imports.upsertCoverage: %s %s: %w", sourceID, mangaURL, err)
	}
	return nil
}

// markCoveragePending records that a computation has started.
func (s *Service) markCoveragePending(ctx context.Context, sourceID, mangaURL string) error {
	return s.upsertCoverage(ctx, sourceID, mangaURL, coverageStatusPending, "", "", nil)
}

// saveCoverage stores a completed breakdown and stamps its as-of instant.
func (s *Service) saveCoverage(ctx context.Context, sourceID, mangaURL string, dto SourceBreakdownDTO) error {
	encoded, err := json.Marshal(dto)
	if err != nil {
		return fmt.Errorf("imports.saveCoverage: encode %s %s: %w", sourceID, mangaURL, err)
	}
	now := time.Now().UTC()
	return s.upsertCoverage(ctx, sourceID, mangaURL, coverageStatusReady, string(encoded), "", &now)
}

// failCoverage records that a computation failed, and why. The reason is shown
// to the owner — an empty panel with no explanation is the outcome this avoids.
// computed_at is explicitly CLEARED (passed as nil to upsertCoverage), never
// left at a previous successful run's timestamp: it is the as-of of the
// STORED PAYLOAD, a failed run has no payload, and a stale-but-plausible as-of
// beside a failure is worse than none — the entire reason this snapshot is
// persisted rather than just cached is that the as-of tells the owner how
// stale it is.
func (s *Service) failCoverage(ctx context.Context, sourceID, mangaURL string, cause error) error {
	return s.upsertCoverage(ctx, sourceID, mangaURL, coverageStatusFailed, "", cause.Error(), nil)
}
