package downloads_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	downloadssvc "github.com/technobecet/tsundoku/internal/downloads"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// redownloadCutoff is the "downloaded since" instant the handler fixtures sit
// around, and the value every request in this file sends as ?since.
var redownloadCutoff = time.Date(2026, 7, 25, 8, 39, 52, 0, time.UTC)

// sinceParam renders the cutoff exactly as the API expects it (RFC 3339).
func sinceParam() string {
	return url.QueryEscape(redownloadCutoff.Format(time.RFC3339))
}

// seedRedownloadable adds a DOWNLOADED chapter to the seeded series — satisfied by
// a "Comix" source and written after the cutoff — so the re-download routes have a
// real target. It returns the chapter's id.
func (env *testEnv) seedRedownloadable(ctx context.Context, t *testing.T) uuid.UUID {
	t.Helper()
	seriesID := firstSeriesID(ctx, t, env.client)
	prov := env.client.SeriesProvider.Create().
		SetSeriesID(seriesID).SetProvider("42").SetProviderName("Comix").
		SetImportance(20).SaveX(ctx)
	env.client.ProviderChapter.Create().
		SetSeriesProviderID(prov.ID).SetChapterKey("ch-3").SetAttempts(2).SaveX(ctx)
	return env.client.Chapter.Create().
		SetSeriesID(seriesID).SetChapterKey("ch-3").SetNumber(3).
		SetState(entchapter.StateDownloaded).
		SetSatisfiedByProviderID(prov.ID).
		SetFilename("[42][en] Solo Leveling 003.cbz").
		SetDownloadDate(redownloadCutoff.Add(time.Hour)).
		SaveX(ctx).ID
}

// TestRedownloadChapter_OK proves the per-chapter route re-queues a downloaded
// chapter (204), keeps its filename, and fires the auto-converge trigger so the
// re-download starts on the next pass rather than the next timer tick.
func TestRedownloadChapter_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	id := env.seedRedownloadable(ctx, t)

	before := env.triggerCalls
	rec := env.do(http.MethodPost, "/api/chapters/"+id.String()+"/redownload")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.triggerCalls != before+1 {
		t.Errorf("trigger calls = %d; want %d (a successful re-download starts a cycle)", env.triggerCalls, before+1)
	}
	got := env.client.Chapter.GetX(ctx, id)
	if got.State != entchapter.StateWanted {
		t.Errorf("state = %s; want wanted", got.State)
	}
	if got.Filename == "" {
		t.Error("filename was cleared; the existing CBZ must stay addressable until the new one lands")
	}
}

// TestRedownloadChapter_Conflict proves a chapter with no stored CBZ is refused
// with 409 — the re-download admits a different set from the retry.
func TestRedownloadChapter_Conflict(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	before := env.triggerCalls
	rec := env.do(http.MethodPost, "/api/chapters/"+env.wantedID.String()+"/redownload")
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.triggerCalls != before {
		t.Errorf("trigger fired on a rejected re-download (calls %d → %d)", before, env.triggerCalls)
	}
}

// TestRedownloadChapter_NotFoundAndMalformed covers the two remaining per-chapter
// statuses: an unknown id is a 404, a non-UUID id is a 400.
func TestRedownloadChapter_NotFoundAndMalformed(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	if rec := env.do(http.MethodPost, "/api/chapters/"+uuid.New().String()+"/redownload"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id: want 404, got %d", rec.Code)
	}
	if rec := env.do(http.MethodPost, "/api/chapters/not-a-uuid/redownload"); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed id: want 400, got %d", rec.Code)
	}
}

// TestRedownloadPreview_OK proves the preview route answers the matched count
// without mutating anything — the chapter is still downloaded afterwards.
func TestRedownloadPreview_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	id := env.seedRedownloadable(ctx, t)

	rec := env.do(http.MethodGet, "/api/downloads/redownload?source=Comix&since="+sinceParam())
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got downloadssvc.RedownloadPreviewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Matched != 1 {
		t.Errorf("matched = %d; want 1", got.Matched)
	}
	if env.client.Chapter.GetX(ctx, id).State != entchapter.StateDownloaded {
		t.Error("the preview mutated the chapter; it must delete and change nothing")
	}
}

// TestRedownloadAll_OK proves the bulk route re-queues the matching chapters,
// returns the count, and fires the trigger.
func TestRedownloadAll_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	id := env.seedRedownloadable(ctx, t)

	before := env.triggerCalls
	rec := env.do(http.MethodPost, "/api/downloads/redownload?source=Comix&since="+sinceParam())
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got downloadssvc.RedownloadResultDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Requeued != 1 {
		t.Errorf("requeued = %d; want 1", got.Requeued)
	}
	if env.triggerCalls != before+1 {
		t.Errorf("trigger calls = %d; want %d", env.triggerCalls, before+1)
	}
	if env.client.Chapter.GetX(ctx, id).State != entchapter.StateWanted {
		t.Error("the matched chapter was not re-queued")
	}
}

// TestRedownloadAll_ScanlatorIsPresenceBased pins the deliberate query-param
// contract: ?scanlator is matched EXACTLY when present (so an empty value addresses
// the source's all-scanlators provider) and ignored entirely when absent (every
// scanlator of the source). Without presence semantics those two cases would be
// indistinguishable.
func TestRedownloadAll_ScanlatorIsPresenceBased(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	env.seedRedownloadable(ctx, t)

	// The seeded provider has NO scanlator, so an empty ?scanlator matches it…
	rec := env.do(http.MethodGet, "/api/downloads/redownload?source=Comix&scanlator=&since="+sinceParam())
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var all downloadssvc.RedownloadPreviewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if all.Matched != 1 {
		t.Errorf("empty ?scanlator matched %d; want 1 (the all-scanlators provider)", all.Matched)
	}

	// …while a named scanlator matches nothing.
	rec = env.do(http.MethodGet, "/api/downloads/redownload?source=Comix&scanlator=Valir+Scans&since="+sinceParam())
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var named downloadssvc.RedownloadPreviewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &named); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if named.Matched != 0 {
		t.Errorf("?scanlator=Valir Scans matched %d; want 0", named.Matched)
	}
}

// TestRedownloadFilter_Rejects400 proves the filter fails CLOSED: a missing source,
// a missing cutoff or an unparseable cutoff is a 400 on BOTH routes — never a sweep
// with an implied "everything, since forever".
func TestRedownloadFilter_Rejects400(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	cases := []struct{ name, query string }{
		{"no source", "?since=" + sinceParam()},
		{"blank source", "?source=%20&since=" + sinceParam()},
		{"no since", "?source=Comix"},
		{"unparseable since", "?source=Comix&since=yesterday"},
	}
	for _, tc := range cases {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(method+" "+tc.name, func(t *testing.T) {
				rec := env.do(method, "/api/downloads/redownload"+tc.query)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// TestRedownloadRoutes_RequireAuth proves all three new routes sit behind
// RequireOwner.
func TestRedownloadRoutes_RequireAuth(t *testing.T) {
	env := newTestEnv(t)
	cases := []struct{ method, target string }{
		{http.MethodPost, "/api/chapters/" + uuid.New().String() + "/redownload"},
		{http.MethodGet, "/api/downloads/redownload?source=Comix&since=" + sinceParam()},
		{http.MethodPost, "/api/downloads/redownload?source=Comix&since=" + sinceParam()},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			if rec := env.doUnauth(tc.method, tc.target); rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", rec.Code)
			}
		})
	}
}
