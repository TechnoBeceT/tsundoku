package reporting

import (
	"context"
	"fmt"
	"sort"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/ent"
	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
)

// TimelineBucket is one time slot's success/fail tally — the data behind the
// stacked green/red histogram. Bucket is the slot's start (a date_trunc boundary).
type TimelineBucket struct {
	Bucket  time.Time
	Success int
	Failed  int
	Total   int
}

// timelineRow is the scan target for the bucketed aggregate: one row per
// (bucket, status) with its count. bucket is date_trunc(unit, created_at).
type timelineRow struct {
	Bucket time.Time             `json:"bucket"`
	Status entsourceevent.Status `json:"status"`
	Count  int                   `json:"count"`
}

// Timeline returns the window's success/fail counts bucketed by hour or day. The
// bucketing is done IN THE DATABASE via a date_trunc GROUP BY (an Ent aggregate
// modifier — see below) so only the already-summed per-bucket rows cross the
// wire (at most ~30 days × 2 statuses), never the raw events. sourceKey ==
// AllSourcesKey selects the global timeline. Buckets are returned ascending.
func (s *Service) Timeline(ctx context.Context, sourceKey string, bucket Bucket, period Period, now time.Time) ([]TimelineBucket, error) {
	f := EventFilter{SourceKey: sourceKey, Since: period.Since(now)}
	unit := bucket.SQL() // closed set (hour|day) — safe to interpolate.

	var rows []timelineRow
	err := s.client.SourceEvent.Query().
		Where(f.predicates()...).
		GroupBy(entsourceevent.FieldStatus).
		Aggregate(
			ent.As(ent.Count(), "count"),
			// The bucket is a GROUP BY on a date_trunc EXPRESSION, which ent's
			// column-name GroupBy cannot express directly. The AggregateFunc gets
			// the raw sql.Selector: it appends the date_trunc term to the GROUP BY
			// (Selector.GroupBy appends) and returns it as the aliased "bucket"
			// select column. unit is a validated closed literal, and the column is
			// qualified via Selector.C, so the expression carries no user input.
			func(sel *sql.Selector) string {
				expr := fmt.Sprintf("date_trunc('%s', %s)", unit, sel.C(entsourceevent.FieldCreatedAt))
				sel.GroupBy(expr)
				return sql.As(expr, "bucket")
			},
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("reporting.Timeline: bucket aggregate: %w", err)
	}

	return foldTimeline(rows), nil
}

// foldTimeline pivots the (bucket, status) rows into one entry per bucket with
// success + failed counts, sorted ascending by bucket start.
func foldTimeline(rows []timelineRow) []TimelineBucket {
	byBucket := make(map[time.Time]*TimelineBucket)
	for _, r := range rows {
		b := byBucket[r.Bucket]
		if b == nil {
			b = &TimelineBucket{Bucket: r.Bucket}
			byBucket[r.Bucket] = b
		}
		b.Total += r.Count
		switch r.Status {
		case entsourceevent.StatusSuccess:
			b.Success += r.Count
		case entsourceevent.StatusFailed:
			b.Failed += r.Count
		}
	}

	out := make([]TimelineBucket, 0, len(byBucket))
	for _, b := range byBucket {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket.Before(out[j].Bucket) })
	return out
}
