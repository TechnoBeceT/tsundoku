package series_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// TestDetailFeedCountAndRangesMakeNoSourceCalls is the load-bearing proof of the
// provider-feed-count feature: a source's chapter OFFERING (feedCount +
// feedRanges) is served from the ProviderChapter rows we ALREADY store, so
// GET /api/series/:id must make ZERO calls to Suwayomi / the source.
//
// Before this feature the owner had to click "Show coverage", which fired a LIVE
// per-source breakdown fetch to see a number we already held in the DB — a
// needless source ping (ban risk). The assertion below (env.sw.calls == 0 on a
// counting fake client) is what keeps that regression from coming back.
//
// It also asserts the JSON shape end-to-end: the gapped feed 1,2,3,5 renders as
// "1-3, 5", and a provider whose feed is empty reports 0 / "".
func TestDetailFeedCountAndRangesMakeNoSourceCalls(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	s := env.client.Series.Create().
		SetTitle("Feed Series").SetSlug("feed-series").
		SetCategoryID(catID(ctx, env.client, "Manga")).SaveX(ctx)

	fed := env.client.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("mangadex").SetLanguage("en").
		SetSuwayomiID(7).SetImportance(10).SaveX(ctx)
	empty := env.client.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("weeb").SetLanguage("en").
		SetSuwayomiID(9).SetImportance(5).SaveX(ctx)

	for _, key := range []struct {
		key string
		num float64
	}{{"1", 1}, {"2", 2}, {"3", 3}, {"5", 5}} {
		num := key.num
		env.client.ProviderChapter.Create().
			SetSeriesProviderID(fed.ID).SetChapterKey(key.key).SetNumber(num).SaveX(ctx)
	}

	// Reset the counter: only the detail request under test may contribute.
	env.sw.calls = 0

	rec := env.do(http.MethodGet, "/api/series/"+s.ID.String(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/series/:id = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	if env.sw.calls != 0 {
		t.Errorf("GET /api/series/:id made %d Suwayomi call(s), want 0 — the provider feed must come from OUR DB, never a source ping", env.sw.calls)
	}

	var body struct {
		Providers []struct {
			ID         string `json:"id"`
			FeedCount  int    `json:"feedCount"`
			FeedRanges string `json:"feedRanges"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode detail: %v", err)
	}

	byID := make(map[string]struct {
		count  int
		ranges string
	}, len(body.Providers))
	for _, p := range body.Providers {
		byID[p.ID] = struct {
			count  int
			ranges string
		}{p.FeedCount, p.FeedRanges}
	}

	if got := byID[fed.ID.String()]; got.count != 4 || got.ranges != "1-3, 5" {
		t.Errorf("fed provider feedCount/feedRanges = %d/%q, want 4/%q", got.count, got.ranges, "1-3, 5")
	}
	if got := byID[empty.ID.String()]; got.count != 0 || got.ranges != "" {
		t.Errorf("empty-feed provider feedCount/feedRanges = %d/%q, want 0/\"\"", got.count, got.ranges)
	}
}
