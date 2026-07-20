package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/ent"
	entsourcemetric "github.com/technobecet/tsundoku/internal/ent/sourcemetric"
)

// maxErrorLen caps a stored last_error so a pathologically long upstream message
// can't bloat the metric row. Truncated errors keep their prefix (the useful part).
const maxErrorLen = 512

// Service is the concrete Recorder plus the read side of the metrics store. It
// owns the Ent client. The Ent client is safe for concurrent use, so the methods
// are goroutine-safe. Writes are best-effort; reads return errors normally.
type Service struct {
	client *ent.Client
}

// NewService builds a metrics Service over the Ent client.
func NewService(client *ent.Client) *Service {
	return &Service{client: client}
}

// Record folds a single observation into its source's rolling snapshot. It is the
// convenience one-sample form of RecordBatch (e.g. the warm-up job records one
// source at a time). Best-effort: a DB failure is logged and swallowed.
func (s *Service) Record(ctx context.Context, sourceID, sourceName string, latency time.Duration, sourceErr error) {
	s.RecordBatch(ctx, []Sample{{SourceID: sourceID, SourceName: sourceName, Latency: latency, Err: sourceErr}})
}

// RecordBatch folds a slice of observations (one per source) into their rolling
// snapshots. Each sample is upserted in its own short transaction so one bad row
// never drops the others; a per-sample failure is logged and skipped (best-effort:
// metrics bookkeeping must never break or slow the caller).
func (s *Service) RecordBatch(ctx context.Context, samples []Sample) {
	for _, sample := range samples {
		if err := s.recordOne(ctx, sample); err != nil {
			slog.WarnContext(ctx, "metrics: record failed (best-effort, skipping)",
				"source", sample.SourceID, "err", err)
		}
	}
}

// recordOne upserts one sample: it creates the row on first sight (seeding the
// EWMA to the sample) or blends the sample into the existing row. The query and
// write run in one short transaction to shrink the lost-update window between
// concurrent searches on the same source (acceptable-loss best-effort — no row
// lock, since metrics are advisory). A non-positive latency (no meaningful
// timing) bumps only the counters, leaving the EWMA untouched.
func (s *Service) recordOne(ctx context.Context, sample Sample) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("metrics.recordOne: begin tx: %w", err)
	}

	row, err := tx.SourceMetric.Query().
		Where(entsourcemetric.SourceIDEQ(sample.SourceID)).
		Only(ctx)
	switch {
	case ent.IsNotFound(err):
		err = s.createRow(ctx, tx, sample)
	case err != nil:
		err = fmt.Errorf("metrics.recordOne: query %q: %w", sample.SourceID, err)
	default:
		err = s.updateRow(ctx, tx, row, sample)
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metrics.recordOne: commit %q: %w", sample.SourceID, err)
	}
	return nil
}

// createRow inserts the first snapshot for a source, seeding the EWMA to the
// sample latency and setting the success/failure fields from the outcome.
func (s *Service) createRow(ctx context.Context, tx *ent.Tx, sample Sample) error {
	sampleMs := latencyMs(sample.Latency)
	c := tx.SourceMetric.Create().
		SetSourceID(sample.SourceID).
		SetSourceName(sample.SourceName).
		SetSearchCount(1)
	if sampleMs > 0 {
		c.SetEwmaLatencyMs(sampleMs).SetLastLatencyMs(sampleMs)
	}
	applyOutcomeCreate(c, sample.Err, time.Now())
	if err := c.Exec(ctx); err != nil {
		return fmt.Errorf("metrics.recordOne: create %q: %w", sample.SourceID, err)
	}
	return nil
}

// updateRow blends a sample into an existing snapshot: it bumps search_count,
// updates the EWMA/last latency (when the sample carries a meaningful latency),
// and updates the success/failure fields.
func (s *Service) updateRow(ctx context.Context, tx *ent.Tx, row *ent.SourceMetric, sample Sample) error {
	sampleMs := latencyMs(sample.Latency)
	u := tx.SourceMetric.UpdateOne(row).
		SetSourceName(sample.SourceName).
		AddSearchCount(1)
	if sampleMs > 0 {
		u.SetLastLatencyMs(sampleMs).SetEwmaLatencyMs(NextEwma(row.EwmaLatencyMs, sampleMs))
	}
	applyOutcomeUpdate(u, sample.Err, time.Now())
	if err := u.Exec(ctx); err != nil {
		return fmt.Errorf("metrics.recordOne: update %q: %w", sample.SourceID, err)
	}
	return nil
}

// applyOutcomeCreate stamps the success or failure fields on a create builder.
func applyOutcomeCreate(c *ent.SourceMetricCreate, sourceErr error, now time.Time) {
	if sourceErr != nil {
		c.SetFailCount(1).SetLastError(truncateError(sourceErr)).SetLastErrorAt(now)
		return
	}
	c.SetSuccessCount(1).SetLastSuccessAt(now)
}

// applyOutcomeUpdate stamps the success or failure fields on an update builder.
func applyOutcomeUpdate(u *ent.SourceMetricUpdateOne, sourceErr error, now time.Time) {
	if sourceErr != nil {
		u.AddFailCount(1).SetLastError(truncateError(sourceErr)).SetLastErrorAt(now)
		return
	}
	u.AddSuccessCount(1).SetLastSuccessAt(now)
}

// SetWarmed stamps last_warmed_at (and refreshes source_name), upserting the row
// so a source warmed before it was ever searched still gets a snapshot. It does
// NOT touch the counters or EWMA — a warm is not a measured search. Unlike the
// Record path it returns its error so the warm-up job can log it.
func (s *Service) SetWarmed(ctx context.Context, sourceID, sourceName string, at time.Time) error {
	row, err := s.client.SourceMetric.Query().
		Where(entsourcemetric.SourceIDEQ(sourceID)).
		Only(ctx)
	if ent.IsNotFound(err) {
		if cErr := s.client.SourceMetric.Create().
			SetSourceID(sourceID).
			SetSourceName(sourceName).
			SetLastWarmedAt(at).
			Exec(ctx); cErr != nil {
			return fmt.Errorf("metrics.SetWarmed: create %q: %w", sourceID, cErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("metrics.SetWarmed: query %q: %w", sourceID, err)
	}
	if uErr := s.client.SourceMetric.UpdateOne(row).
		SetSourceName(sourceName).
		SetLastWarmedAt(at).
		Exec(ctx); uErr != nil {
		return fmt.Errorf("metrics.SetWarmed: update %q: %w", sourceID, uErr)
	}
	return nil
}

// Delete removes the rolling snapshot row for sourceID and returns how many rows
// were deleted (0 when the source was never measured, else 1). Unlike the
// best-effort Record* writes this RETURNS its error: it is called from the
// owner-initiated source purge, which must report exactly what it removed
// (§16 — no silent operation). Idempotent — deleting zero rows is not an error.
func (s *Service) Delete(ctx context.Context, sourceID string) (int, error) {
	n, err := s.client.SourceMetric.Delete().
		Where(entsourcemetric.SourceIDEQ(sourceID)).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("metrics.Delete: delete %q: %w", sourceID, err)
	}
	return n, nil
}

// List returns every source metric sorted by EWMA latency descending (slowest
// first) — the order the source-metrics screen renders.
func (s *Service) List(ctx context.Context) ([]*ent.SourceMetric, error) {
	rows, err := s.client.SourceMetric.Query().
		Order(entsourcemetric.ByEwmaLatencyMs(sql.OrderDesc())).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("metrics.List: %w", err)
	}
	return rows, nil
}

// Snapshot returns every source metric keyed by source_id — the lookup the
// warm-up job uses to decide which sources are slow or never-measured.
func (s *Service) Snapshot(ctx context.Context) (map[string]*ent.SourceMetric, error) {
	rows, err := s.client.SourceMetric.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("metrics.Snapshot: %w", err)
	}
	out := make(map[string]*ent.SourceMetric, len(rows))
	for _, r := range rows {
		out[r.SourceID] = r
	}
	return out, nil
}

// latencyMs converts a duration to whole milliseconds, clamping a negative value
// to 0 (guards a malformed sample from corrupting the EWMA).
func latencyMs(d time.Duration) int {
	ms := int(d.Milliseconds())
	if ms < 0 {
		return 0
	}
	return ms
}

// truncateError renders an error, capping the stored message at maxErrorLen.
func truncateError(err error) string {
	msg := err.Error()
	if len(msg) > maxErrorLen {
		return msg[:maxErrorLen]
	}
	return msg
}
