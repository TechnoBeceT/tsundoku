package imports_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sse"
)

const (
	testCoverageSourceID = "42"
	testCoverageMangaURL = "/qly0d-apotheosis"
)

// TestComputeCoveragePersistsAndAnnounces proves the job writes a READY
// snapshot and emits exactly one terminal event, so a client that missed the
// HTTP response still learns the outcome (the library.dedup.done contract).
func TestComputeCoveragePersistsAndAnnounces(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	hub := sse.NewHub()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	svc := newServiceWithChapters(t, client, hub, 1301) // helper: see below

	if err := svc.ComputeCoverage(ctx, testCoverageSourceID, testCoverageMangaURL, "Apotheosis"); err != nil {
		t.Fatalf("ComputeCoverage: %v", err)
	}

	snap, ok, err := imports.ExportLoadCoverage(svc, ctx, testCoverageSourceID, testCoverageMangaURL)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if snap.Status != "ready" || snap.Payload.Total != 1301 {
		t.Errorf("snapshot = %+v, want ready with 1301 chapters", snap)
	}

	ev := awaitEvent(t, events, "imports.coverage.done")
	if ev == nil {
		t.Fatal("no imports.coverage.done event — the outcome would be invisible to a client that stopped waiting")
	}
}

// TestComputeCoverageRecordsFailure proves a failed walk is PERSISTED as
// failed with its reason and still announced. Without this the UI shows an
// empty panel forever and the owner cannot tell "computing" from "broken".
func TestComputeCoverageRecordsFailure(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	hub := sse.NewHub()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	svc := newServiceWithFailingChapters(t, client, hub, errors.New("upstream timed out"))

	err := svc.ComputeCoverage(ctx, testCoverageSourceID, "/x", "X")
	if err == nil {
		t.Fatal("ComputeCoverage returned nil on a failing fetch")
	}

	snap, ok, _ := imports.ExportLoadCoverage(svc, ctx, testCoverageSourceID, "/x")
	if !ok || snap.Status != "failed" || snap.LastError == "" {
		t.Errorf("snapshot = %+v, want a failed snapshot carrying the reason", snap)
	}
	if ev := awaitEvent(t, events, "imports.coverage.done"); ev == nil {
		t.Error("a failed computation must still announce, or the UI waits forever")
	}
}

// TestComputeCoverageAnnouncesWhenMarkPendingFails proves the FIRST exit path
// — markCoveragePending's own upsert failing, before the chapter walk ever
// runs — still emits imports.coverage.done. Neither test above drives this:
// both seed a store that accepts the initial pending write. Without this, a
// transient DB hiccup on that very first write would leave a detached caller
// (the entire reason this computation is backgrounded) with ZERO signal —
// watching a panel that never resolves.
//
// The failure is induced by closing the underlying *sql.DB testdb.NewWithSQL
// hands back, so every subsequent ent query (starting with
// markCoveragePending's own) fails with "sql: database is closed". This is
// the harness-provided seam the review asked for — no production-only
// scaffolding was added to make the store injectable.
func TestComputeCoverageAnnouncesWhenMarkPendingFails(t *testing.T) {
	client, db := testdb.NewWithSQL(t)
	ctx := context.Background()
	hub := sse.NewHub()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	svc := newServiceWithChapters(t, client, hub, 10)

	if err := db.Close(); err != nil {
		t.Fatalf("close underlying db: %v", err)
	}

	err := svc.ComputeCoverage(ctx, testCoverageSourceID, testCoverageMangaURL, "Apotheosis")
	if err == nil {
		t.Fatal("ComputeCoverage returned nil with the store unreachable")
	}

	if ev := awaitEvent(t, events, "imports.coverage.done"); ev == nil {
		t.Error("a markCoveragePending failure must still announce, or a detached caller waits forever")
	}
}

// newServiceWithChapters builds a Service whose engine client returns `count`
// synthetic chapters split across two scanlators, wired to hub so the
// terminal event is observable. Mirrors cache_test.go's construction — note
// the db is the THIRD argument, and testSearchTimeout is declared in
// service_test.go. WithSources seeds the source registry: SourceBreakdown
// resolves the source through Client.Sources before it ever fetches a
// chapter, so a bare WithChapters fake (no matching Source row) would fail
// every computation with ErrSourceNotFound before the chapter walk even runs.
func newServiceWithChapters(t *testing.T, db *ent.Client, hub *sse.Hub, count int) *imports.Service {
	t.Helper()
	sourceID, err := strconv.ParseInt(testCoverageSourceID, 10, 64)
	if err != nil {
		t.Fatalf("source id: %v", err)
	}

	chapters := make([]sourceengine.Chapter, 0, count)
	for i := 1; i <= count; i++ {
		scanlator := "Alpha"
		if i > count/2 {
			scanlator = "Beta"
		}
		chapters = append(chapters, sourceengine.Chapter{
			URL:       fmt.Sprintf("/c/%d", i),
			Name:      fmt.Sprintf("Chapter %d", i),
			Number:    float64(i), // a plain float64, NOT a pointer
			Scanlator: scanlator,
		})
	}

	fc := fake.New(
		fake.WithSources([]sourceengine.Source{{ID: sourceID, Name: "Test Source"}}),
		fake.WithChapters(sourceID, testCoverageMangaURL, chapters),
	)
	return imports.NewService(fc, nil, db, t.TempDir(), testSearchTimeout, nil).WithHub(hub)
}

// newServiceWithFailingChapters builds a Service whose chapter fetch always
// errors. fake.WithError keys on the exported METHOD NAME, not a source id;
// WithSources is still required so resolveSource succeeds and the failure
// genuinely comes from the Chapters call SourceBreakdown makes next.
func newServiceWithFailingChapters(t *testing.T, db *ent.Client, hub *sse.Hub, cause error) *imports.Service {
	t.Helper()
	sourceID, err := strconv.ParseInt(testCoverageSourceID, 10, 64)
	if err != nil {
		t.Fatalf("source id: %v", err)
	}

	fc := fake.New(
		fake.WithSources([]sourceengine.Source{{ID: sourceID, Name: "Test Source"}}),
		fake.WithError("Chapters", cause),
	)
	return imports.NewService(fc, nil, db, t.TempDir(), testSearchTimeout, nil).WithHub(hub)
}

// awaitEvent reads the hub channel until an event named `name` arrives or the
// deadline passes, returning nil on timeout. Deadline-based, never
// time.Sleep — a sleep would either flake or slow every run.
func awaitEvent(t *testing.T, events <-chan sse.Event, name string) *sse.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type == name {
				return &ev
			}
		case <-deadline:
			return nil
		}
	}
}
