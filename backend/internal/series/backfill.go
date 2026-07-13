package series

import (
	"context"
	"database/sql"
	"fmt"
)

// BackfillFirstDownloadedAt is the one-time migration that seeds the new
// `first_downloaded_at` column from the existing `download_date` column for
// every chapter already in the database.
//
// WHY this exists: `download_date` is a FETCH timestamp — it is rewritten
// every time a chapter's CBZ is (re)written, including a Library-Convergence
// upgrade that re-fetches an OLD chapter from a better source
// (download/upgrade.go). `first_downloaded_at` exists precisely because
// `download_date` cannot answer "when did this chapter first become
// readable" — it is written WRITE-ONCE on a chapter's first successful
// download (see the field's doc comment on the Chapter schema) and never
// touched again. A brand-new chapter earns a correct `first_downloaded_at`
// for free going forward; this backfill is what gives the EXISTING back
// catalogue a value on day one, instead of every row reading "unknown" until
// it next happens to get a new chapter.
//
// THE HONESTY CAVEAT (owner-accepted): by the time this feature shipped, past
// convergence upgrades had already overwritten `download_date` on plenty of
// old chapters — so for the back catalogue, the value this backfill seeds is
// an APPROXIMATION of the true first-arrival time, not a guarantee of it. The
// real original arrival time was never recorded anywhere before this feature
// and is not recoverable. From this field's introduction forward, every write
// is exact (write-once, never rewritten). Never "fix" the approximation by
// inventing a timestamp for a row that has none — see the next paragraph.
//
// A chapter with NEITHER a `download_date` NOR a `first_downloaded_at` has NO
// evidence of when it arrived (e.g. never downloaded) and is deliberately
// left NULL by this backfill — it sorts last as "unknown" in the recently-
// updated view. Stamping such a row with time.Now() would invent a plausible-
// but-false timestamp, exactly the failure mode this feature exists to
// prevent.
//
// It is write-once-safe and idempotent by construction: the WHERE clause only
// ever touches rows where `first_downloaded_at IS NULL`, so a row that
// already carries a value (backfilled by an earlier run, or written by the
// normal first-download path) is never overwritten, and running this twice
// updates zero rows the second time.
//
// It takes the raw *sql.DB because Ent's fluent builder has no cross-column
// SET (`SET first_downloaded_at = download_date`) — mirrors
// category.BackfillSeries and library.DropLegacyImportEntryColumns, the
// existing raw-SQL post-migration cleanup precedents.
//
// The returned count is the number of rows updated, for the caller to log
// once at startup.
func BackfillFirstDownloadedAt(ctx context.Context, db *sql.DB) (int64, error) {
	res, err := db.ExecContext(ctx, `
		UPDATE chapters
		   SET first_downloaded_at = download_date
		 WHERE first_downloaded_at IS NULL
		   AND download_date IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("series.BackfillFirstDownloadedAt: backfill update: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		// UNCOVERABLE: the pgx stdlib driver always supports RowsAffected for an
		// UPDATE; this branch exists only for interface-contract completeness.
		return 0, fmt.Errorf("series.BackfillFirstDownloadedAt: rows affected: %w", err)
	}
	return rows, nil
}
