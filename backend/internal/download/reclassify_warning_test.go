// Package download_test — the OBSERVABILITY half of the ratified 502-unwrap
// trade (GAP-141, ratified as QCAT-361).
//
// The unwrap is what stops the engine host's "status 502" envelope masking the
// source's real error, and the owner ratified KEEPING it knowing the residual
// risk: a 502-wrapped parse / not-found / no-pages message now flips from
// SOURCE-WIDE to CHAPTER-SPECIFIC, which spends the (chapter,source) attempts
// budget WITHOUT tripping the breaker. If a source starts serving an HTML
// interstitial where content was expected, its whole queue drains toward
// permanently_failed with no cooldown and no health signal.
//
// The ratification rests on that being OBSERVABLE, and the warning
// chargeFetchFailure emits is the entire observability. TestReclassifiedByUnwrap
// pins the PREDICATE; these tests pin that the dispatcher actually CALLS it — the
// mitigation was otherwise free to be refactored away with the suite green.
//
// Requires Docker (via testcontainers).
package download_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// reclassifySentinel is a stable substring of the warning chargeFetchFailure
// emits when the 502 unwrap changed the verdict. Matching a fragment rather than
// the whole line keeps a wording tweak from breaking the test while still
// failing loudly if the line stops being emitted at all.
const reclassifySentinel = "unwrapping the 502 envelope reclassified"

// lockedBuffer is a goroutine-safe log sink. The dispatcher fetches on its own
// goroutines, so the default logger can be written concurrently even at
// concurrency 1 (progress + cycle bookkeeping share the same logger).
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p to the buffer under the lock.
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything written so far.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureWarnings redirects the default slog logger into a fresh buffer for the
// rest of the test and restores the previous logger in t.Cleanup. The dispatcher
// warns through slog's package-level functions, so this is the only seam.
func captureWarnings(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// runOneFailedCycle drives ONE real download cycle whose single candidate fails
// with cause, and returns everything the default logger emitted while it ran. The
// logger is captured only after seeding, so the buffer holds the cycle's output
// and nothing else.
//
// It also asserts the failure took the CHAPTER-SPECIFIC arm (attempts 0→1), which
// is what stops either caller passing vacuously: an absent warning proves nothing
// if the cycle never reached the arm that emits it.
func runOneFailedCycle(t *testing.T, cause error) string {
	t.Helper()
	ctx := context.Background()
	client := testdb.New(t)
	_, pc := singleSourceChapter(ctx, t, client)

	logged := captureWarnings(t)
	rs := classifiedSettings()
	d := download.New(client, fake.New(fake.WithError(cause)), sse.NewHub(),
		download.Config{Storage: mustTempDir(t)}, rs, sourcegate.NewService(client, rs))
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if a := client.ProviderChapter.GetX(ctx, pc.ID).Attempts; a != 1 {
		t.Fatalf("attempts = %d, want 1 — the cycle did not take the chapter-specific arm, so the warning assertion would be meaningless", a)
	}
	return logged.String()
}

// TestChargeFetchFailure_WarnsWhenUnwrapReclassifies proves the ratified risk is
// actually surfaced END TO END: a real 502-enveloped `parse` failure, driven
// through the dispatcher, leaves the warning in the log.
//
// The chosen cause is the exact dangerous shape — an extension exception whose
// envelope reads server_error (source-wide) but whose own message reads parse
// (chapter-specific). Without the warning, a source that began answering with an
// HTML interstitial would drain its queue silently; the log line is the only
// place that shows up before the library is already thin.
func TestChargeFetchFailure_WarnsWhenUnwrapReclassifies(t *testing.T) {
	logged := runOneFailedCycle(t, &sourceengine.UpstreamError{
		Status: 502,
		Msg:    "could not parse chapter page: unexpected end of json",
	})

	if !strings.Contains(logged, reclassifySentinel) {
		t.Fatalf("the reclassification warning was never emitted — the ratified residual risk is now invisible; log=%q", logged)
	}
}

// TestChargeFetchFailure_OrdinaryChapterSpecificFailureIsSilent is the other half,
// and it is what stops the test above passing for the wrong reason: a plain
// chapter-specific failure carries no envelope to strip, so nothing was
// reclassified and nothing must be warned about.
//
// A signal that fires on the ordinary case is a signal nobody reads — which
// would cost exactly the observability the owner ratified the unwrap on. The
// cause here takes the SAME chapter-specific arm as the test above, so only the
// reclassifiedByUnwrap guard separates the two outcomes.
func TestChargeFetchFailure_OrdinaryChapterSpecificFailureIsSilent(t *testing.T) {
	logged := runOneFailedCycle(t, errors.New("malformed response body"))

	if strings.Contains(logged, reclassifySentinel) {
		t.Fatalf("an ordinary chapter-specific failure raised the reclassification warning — the signal is noise; log=%q", logged)
	}
}
