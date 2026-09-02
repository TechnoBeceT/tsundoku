package download_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// TestDispatcher_StaleOfferFromEngineHTTPIsChapterSpecific pins the complete
// engine-host HTTP boundary: the refreshed-list stale-offer message must spend
// this provider chapter's retry budget without pausing the whole source, and no
// image request may follow a failed page-resolution call.
func TestDispatcher_StaleOfferFromEngineHTTPIsChapterSpecific(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := testdb.New(t)

	var imageCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/pages":
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "chapter not found in refreshed chapter list: /chapter/stale",
			})
		case "/image":
			imageCalls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected engine request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	series := client.Series.Create().SetTitle("Stale Offer").SetSlug("stale-offer").SaveX(ctx)
	provider := client.SeriesProvider.Create().
		SetSeries(series).
		SetProvider("6311653253665366075").
		SetProviderName("Hive Scans").
		SetURL("/series/study-group").
		SetImportance(10).
		SaveX(ctx)
	providerChapter := client.ProviderChapter.Create().
		SetSeriesProvider(provider).
		SetChapterKey("1").
		SetURL("/chapter/stale").
		SetProviderIndex(0).
		SaveX(ctx)
	client.Chapter.Create().SetSeries(series).SetChapterKey("1").SaveX(ctx)

	runtime := settings.Static{
		Retries:              3,
		Backoff:              time.Hour,
		SourcesFailureThresh: 3,
		SourcesCooldownIv:    time.Hour,
	}
	gate := sourcegate.NewService(client, runtime)
	engine := sourceengine.New(server.URL, server.Client(), "test-engine-control-token")
	dispatcher := download.New(
		client,
		sourceengine.NewFetcher(engine, t.TempDir()),
		sse.NewHub(),
		download.Config{Storage: mustTempDir(t)},
		runtime,
		gate,
	)

	if _, err := dispatcher.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.ProviderChapter.GetX(ctx, providerChapter.ID)
	if got.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", got.Attempts)
	}
	if !gate.IsAvailable(ctx, "Hive Scans", time.Now()) {
		t.Fatal("source breaker is unavailable after a chapter-specific stale offer")
	}
	if got := imageCalls.Load(); got != 0 {
		t.Fatalf("image calls = %d, want 0", got)
	}
}

// TestDispatcher_SourceWidePagesErrorsFromEngineHTTPDoNotSpendAttempts proves the opposite wire
// classification: a 429 or timeout emitted by /pages leaves the provider chapter's retry budget
// untouched and trips the source gate. Kotlin RPC coverage proves engine-side serialization; this
// fake-server test proves the downloader keeps those source-wide responses out of the retry budget.
func TestDispatcher_SourceWidePagesErrorsFromEngineHTTPDoNotSpendAttempts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		error string
	}{
		{name: "rate_limit", error: "429 too many requests"},
		{name: "timeout", error: "request timed out: deadline exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := testdb.New(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/pages" {
					t.Errorf("unexpected engine request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": tc.error})
			}))
			t.Cleanup(server.Close)

			series := client.Series.Create().SetTitle("Source Wide Pages").SetSlug("source-wide-pages-" + tc.name).SaveX(ctx)
			provider := client.SeriesProvider.Create().
				SetSeries(series).
				SetProvider("6311653253665366075").
				SetProviderName("Hive Scans").
				SetURL("/series/study-group").
				SetImportance(10).
				SaveX(ctx)
			providerChapter := client.ProviderChapter.Create().
				SetSeriesProvider(provider).
				SetChapterKey("1").
				SetURL("/chapter/current").
				SetProviderIndex(0).
				SaveX(ctx)
			client.Chapter.Create().SetSeries(series).SetChapterKey("1").SaveX(ctx)

			runtime := settings.Static{
				Retries:              3,
				Backoff:              time.Hour,
				SourcesFailureThresh: 1,
				SourcesCooldownIv:    time.Hour,
			}
			gate := sourcegate.NewService(client, runtime)
			dispatcher := download.New(
				client,
				sourceengine.NewFetcher(sourceengine.New(server.URL, server.Client(), "test-engine-control-token"), t.TempDir()),
				sse.NewHub(),
				download.Config{Storage: mustTempDir(t)},
				runtime,
				gate,
			)

			if _, err := dispatcher.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}

			got := client.ProviderChapter.GetX(ctx, providerChapter.ID)
			if got.Attempts != 0 {
				t.Fatalf("attempts = %d, want 0 (source-wide page failures must not spend retry budget)", got.Attempts)
			}
			if gate.IsAvailable(ctx, "Hive Scans", time.Now()) {
				t.Fatal("source breaker is available after a source-wide page failure")
			}
		})
	}
}
