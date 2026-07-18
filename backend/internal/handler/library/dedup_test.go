package library_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/series"
)

// providerDedupResult mirrors the handler's providerDedupResponse wire shape so
// the round-trip test can decode and assert on it.
type providerDedupResult struct {
	Merged  int                    `json:"merged"`
	Skipped int                    `json:"skipped"`
	Series  series.SeriesDetailDTO `json:"series"`
}

// TestDedupProviders_Happy exercises the full HTTP round-trip: a series carrying
// a disk-origin provider plus its already-drifted linked twin (same display
// name + scanlator, feed present) is collapsed to ONE provider — the response
// echoes merged=1/skipped=0 and carries the refreshed single-provider detail.
func TestDedupProviders_Happy(t *testing.T) {
	env := newEnvWithStorageSeeded(t)
	seriesID := seedDriftedSeries(t, env)

	rec := env.do("POST", fmt.Sprintf("/api/series/%s/providers/dedup", seriesID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("dedup = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	var got providerDedupResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertDedupResult(t, got)
}

// seedDriftedSeries disk-imports the seeded on-disk series (creating the
// disk-origin provider + two downloaded chapters + their CBZs) and manually
// attaches a drifted linked twin (a real source whose provider_name + scanlator
// match the disk row, with a feed for the same two chapter keys). Returns the
// series id — the exact source-identity drift dedup must clean up.
func seedDriftedSeries(t *testing.T, env *testEnv) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if _, err := env.svc.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	entries, err := env.svc.ListImports(ctx, "pending", "", 0, 0)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListImports: %v (entries=%v)", err, entries)
	}
	if _, err := env.svc.Import(ctx, entries[0].Path, nil); err != nil {
		t.Fatalf("Import: %v", err)
	}

	ser := env.client.Series.Query().OnlyX(ctx)
	live := env.client.SeriesProvider.Create().
		SetSeriesID(ser.ID).
		// Provider is a numeric source id string — the live-provider marker
		// under the new identity model (see series.IsLinkedProvider).
		SetProvider("99").
		SetProviderName("mangadex").
		SetScanlator("Alpha").
		SetImportance(5).
		SaveX(ctx)
	one, two := 1.0, 2.0
	env.client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("1").SetNumber(one).SaveX(ctx)
	env.client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("2").SetNumber(two).SaveX(ctx)
	return ser.ID
}

// assertDedupResult checks the successful-dedup wire shape: exactly one pair
// merged, none skipped, and a single remaining LINKED provider (the disk row
// folded away).
func assertDedupResult(t *testing.T, got providerDedupResult) {
	t.Helper()
	if got.Merged != 1 || got.Skipped != 0 {
		t.Fatalf("result = (merged=%d, skipped=%d), want (1, 0)", got.Merged, got.Skipped)
	}
	if len(got.Series.Providers) != 1 {
		t.Fatalf("series providers = %d, want 1 (disk row folded away)", len(got.Series.Providers))
	}
	if p := got.Series.Providers[0]; !p.Linked || p.Provider != "99" {
		t.Fatalf("remaining provider = %+v, want linked=true provider=99", p)
	}
}

// TestDedupProviders_UnknownSeries404 proves an unknown series id maps to 404
// via ErrSeriesNotFound.
func TestDedupProviders_UnknownSeries404(t *testing.T) {
	env := newEnv(t)
	rec := env.do("POST", fmt.Sprintf("/api/series/%s/providers/dedup", uuid.New()), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown series: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestDedupProviders_BadID proves :id is validated as a UUID (400).
func TestDedupProviders_BadID(t *testing.T) {
	env := newEnv(t)
	rec := env.do("POST", "/api/series/not-a-uuid/providers/dedup", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}
