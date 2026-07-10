// Package suwayomi_test — Ingest cache (Task C2) + gate (Task B) integration.
package suwayomi_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	entsourcecircuitstate "github.com/technobecet/tsundoku/internal/ent/sourcecircuitstate"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// countingClient wraps an ingestStubClient and counts FetchChapters calls so a
// test can prove the chapter cache (Task C2) and the gate refusal (Task B) skip
// the upstream fetch.
type countingClient struct {
	*ingestStubClient
	mu    sync.Mutex
	calls int
}

func (c *countingClient) FetchChapters(ctx context.Context, mangaID int) ([]suwayomi.Chapter, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.ingestStubClient.FetchChapters(ctx, mangaID)
}

func (c *countingClient) fetchCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// TestIngest_AddSeries_CachesFetch proves Task C2: with a shared chapter cache,
// two AddSeries for the same (source, manga) trigger exactly ONE upstream
// FetchChapters — the second is served from the cache (and is still an idempotent
// no-op on the DB).
func TestIngest_AddSeries_CachesFetch(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID    = 55
		mangaTitle = "Cached Manga"
		sourceID   = "src-1"
		k          = 3
	)
	sc := &countingClient{ingestStubClient: &ingestStubClient{
		chapters:  makeChapters(k),
		mangaMeta: suwayomi.Manga{Title: mangaTitle},
	}}

	cache := suwayomi.NewChapterCacheConst(time.Minute)
	ing := suwayomi.NewIngestWithGate(sc, client, cache, nil)

	first, err := ing.AddSeries(ctx, sourceID, mangaID, mangaTitle, "")
	if err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}
	if first.NewChapters != k {
		t.Fatalf("first NewChapters = %d, want %d", first.NewChapters, k)
	}
	second, err := ing.AddSeries(ctx, sourceID, mangaID, mangaTitle, "")
	if err != nil {
		t.Fatalf("second AddSeries: %v", err)
	}
	if second.NewChapters != 0 {
		t.Errorf("second NewChapters = %d, want 0 (idempotent)", second.NewChapters)
	}
	if got := sc.fetchCalls(); got != 1 {
		t.Fatalf("FetchChapters called %d times, want 1 (cache must serve the 2nd)", got)
	}
}

// TestIngest_AddSeries_GatedWhenCooledDown proves Task B: when the source's
// circuit-breaker is in cooldown, AddSeries refuses with ErrSourceCooledDown and
// makes NO upstream fetch.
func TestIngest_AddSeries_GatedWhenCooledDown(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID  = 7
		title    = "Blocked"
		sourceID = "src-blocked"
	)
	sc := &countingClient{ingestStubClient: &ingestStubClient{
		chapters:  makeChapters(2),
		mangaMeta: suwayomi.Manga{Title: title},
	}}

	// Threshold 1 ⇒ a single recorded failure trips the breaker into cooldown.
	// No sources resolved ⇒ the gate key is the raw source id (providerName "").
	gate := sourcegate.NewService(client, settings.Static{SourcesFailureThresh: 1, SourcesCooldownIv: time.Hour})
	gate.RecordFailure(ctx, sourceID, errors.New("cf block"), time.Now())

	ing := suwayomi.NewIngestWithGate(sc, client, suwayomi.NewChapterCacheConst(time.Minute), gate)
	_, err := ing.AddSeries(ctx, sourceID, mangaID, title, "")
	if !errors.Is(err, suwayomi.ErrSourceCooledDown) {
		t.Fatalf("AddSeries err = %v, want ErrSourceCooledDown", err)
	}
	if got := sc.fetchCalls(); got != 0 {
		t.Errorf("FetchChapters called %d times, want 0 (gate must refuse before fetch)", got)
	}
}

// TestIngest_AddSeries_HealthySourceProceeds proves the gate's happy path: a
// source with no tripped breaker is fetched and the success is recorded (the
// breaker stays available).
func TestIngest_AddSeries_HealthySourceProceeds(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID  = 8
		title    = "Healthy"
		sourceID = "src-ok"
	)
	sc := &countingClient{ingestStubClient: &ingestStubClient{
		chapters:  makeChapters(2),
		mangaMeta: suwayomi.Manga{Title: title},
	}}
	gate := sourcegate.NewService(client, settings.Static{SourcesFailureThresh: 3, SourcesCooldownIv: time.Hour})

	ing := suwayomi.NewIngestWithGate(sc, client, suwayomi.NewChapterCacheConst(time.Minute), gate)
	if _, err := ing.AddSeries(ctx, sourceID, mangaID, title, ""); err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if got := sc.fetchCalls(); got != 1 {
		t.Fatalf("FetchChapters called %d times, want 1", got)
	}
	if !gate.IsAvailable(ctx, sourceID, time.Now()) {
		t.Error("source breaker should be available after a successful fetch")
	}
	// RecordSuccess ran ⇒ a breaker row exists with 0 consecutive failures.
	row := client.SourceCircuitState.Query().Where(entsourcecircuitstate.SourceKeyEQ(sourceID)).OnlyX(ctx)
	if row.ConsecutiveFailures != 0 {
		t.Errorf("consecutive_failures = %d, want 0", row.ConsecutiveFailures)
	}
}
