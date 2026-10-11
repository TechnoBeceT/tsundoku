package engine_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	handler "github.com/technobecet/tsundoku/internal/handler/engine"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
)

// sourcesCap is the fixed per-source download-concurrency cap the test wires, so
// every downloading row must report cap == sourcesCap.
const sourcesCap = 5

// sourcesEnv wires an Echo instance with GET /api/engine/sources behind
// RequireOwner over a fresh testdb client, with the three live-status ports
// attached (downloads read-model, circuit-breaker snapshot, a fixed cap).
type sourcesEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
}

func newSourcesEnv(t *testing.T) *sourcesEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	static := settings.Static{DownloadConc: sourcesCap}
	h := handler.NewHandler(apkcache.New(t.TempDir()), client).
		WithSourceStatus(downloads.NewService(client), sourcegate.NewService(client, static), static).
		WithSourceCaps(sourcethroughput.NewService(client, static))

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/engine/sources", h.Sources)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &sourcesEnv{e: e, client: client, token: token}
}

func (env *sourcesEnv) get(t *testing.T, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/engine/sources", nil)
	if withAuth {
		r.Header.Set("Authorization", "Bearer "+env.token)
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// TestSources_Unauthorized proves the route is behind RequireOwner.
func TestSources_Unauthorized(t *testing.T) {
	env := newSourcesEnv(t)
	rec := env.get(t, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", rec.Code)
	}
}

// TestSources_EmptyIsEmptyArray proves an idle library returns a valid 200 with an
// empty (non-null) array.
func TestSources_EmptyIsEmptyArray(t *testing.T) {
	env := newSourcesEnv(t)
	rec := env.get(t, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "[]\n" {
		t.Errorf("body = %q, want an empty array", got)
	}
}

// TestSources_DownloadingAndCooling proves the strip reports both halves: a source
// actively downloading (N/cap, from the read-model) and a source in an anti-ban
// cooldown (remaining + classified reason, from the breaker), while a fully-idle
// source is omitted and an expired-cooldown source is not "cooling".
func TestSources_DownloadingAndCooling(t *testing.T) {
	env := newSourcesEnv(t)
	ctx := context.Background()
	seedSourcesFixture(ctx, t, env.client, time.Now())

	rec := env.get(t, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []handler.SourceStatusDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d (%v), want 2 (Asura Scans downloading + The Blank cooling)", len(got), got)
	}

	byKey := map[string]handler.SourceStatusDTO{}
	for _, s := range got {
		byKey[s.SourceKey] = s
	}
	assertDownloadingRow(t, byKey["Asura Scans"])
	assertCoolingRow(t, byKey["The Blank"])

	// Ordering: downloading rows sort before cooling rows.
	if got[0].State != "downloading" || got[1].State != "cooling" {
		t.Errorf("order = [%s, %s], want [downloading, cooling]", got[0].State, got[1].State)
	}
}

// seedSourcesFixture seeds one downloading source (Asura Scans, top candidate of a
// two-source series with a downloading chapter), one cooling source (The Blank,
// future breaker cooldown + rate-limit error), and one recovered source whose
// cooldown has EXPIRED (so it must be omitted from the strip).
func seedSourcesFixture(ctx context.Context, t *testing.T, c *ent.Client, now time.Time) {
	t.Helper()
	alpha := c.Series.Create().SetTitle("Alpha").SetSlug("alpha").SaveX(ctx)
	comix := c.SeriesProvider.Create().SetSeries(alpha).SetProvider("a-comix").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	asura := c.SeriesProvider.Create().SetSeries(alpha).SetProvider("a-asura").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)
	c.ProviderChapter.Create().SetSeriesProviderID(comix.ID).SetChapterKey("d-1").SetURL("https://comix/d-1").SetProviderIndex(0).SaveX(ctx)
	c.ProviderChapter.Create().SetSeriesProviderID(asura.ID).SetChapterKey("d-1").SetURL("https://asura/d-1").SetProviderIndex(0).SaveX(ctx)
	c.Chapter.Create().SetSeries(alpha).SetChapterKey("d-1").SetState(entchapter.StateDownloading).SaveX(ctx)

	c.SourceCircuitState.Create().SetSourceKey("The Blank").SetConsecutiveFailures(4).
		SetCooldownUntil(now.Add(12 * time.Minute)).SetLastError("429 rate limit exceeded").SaveX(ctx)
	c.SourceCircuitState.Create().SetSourceKey("Recovered").SetConsecutiveFailures(1).
		SetCooldownUntil(now.Add(-time.Minute)).SetLastError("timeout").SaveX(ctx)
}

// assertDownloadingRow proves the downloading source reports state + N/cap.
func assertDownloadingRow(t *testing.T, dl handler.SourceStatusDTO) {
	t.Helper()
	if dl.State != "downloading" || dl.ActiveCount != 1 || dl.Cap != sourcesCap {
		t.Errorf("Asura Scans = %+v, want state=downloading activeCount=1 cap=%d", dl, sourcesCap)
	}
}

// assertCoolingRow proves the cooling source reports state + remaining seconds +
// classified reason + failure counters.
func assertCoolingRow(t *testing.T, cool handler.SourceStatusDTO) {
	t.Helper()
	if cool.State != "cooling" || cool.ConsecutiveFailures != 4 {
		t.Errorf("The Blank = %+v, want state=cooling consecutiveFailures=4", cool)
	}
	if cool.CooldownRemainingSeconds <= 0 || cool.CooldownRemainingSeconds > 12*60 {
		t.Errorf("The Blank cooldownRemainingSeconds = %d, want in (0, 720]", cool.CooldownRemainingSeconds)
	}
	if cool.Reason != "rate_limit" {
		t.Errorf("The Blank reason = %q, want rate_limit", cool.Reason)
	}
}

func TestSources_EffectivePersistedSourceCap(t *testing.T) {
	env := newSourcesEnv(t)
	ctx := context.Background()
	s := env.client.Series.Create().SetTitle("Comix cap").SetSlug("comix-cap").SaveX(ctx)
	sp := env.client.SeriesProvider.Create().SetSeries(s).SetProvider("7537715367149829912").SetProviderName("Comix").SetImportance(10).SaveX(ctx)
	env.client.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey("1").SetURL("https://example.test/1").SaveX(ctx)
	env.client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetState(entchapter.StateDownloading).SaveX(ctx)
	env.client.SourceCircuitState.Create().SetSourceKey("Inherited").SetCooldownUntil(time.Now().Add(time.Hour)).SaveX(ctx)
	svc := sourcethroughput.NewService(env.client, settings.Static{DownloadConc: sourcesCap})
	assertEffectiveCaps(t, ctx, env, svc)
}

func assertEffectiveCaps(t *testing.T, ctx context.Context, env *sourcesEnv, svc *sourcethroughput.Service) {
	t.Helper()
	for _, want := range []int{3, 2, sourcesCap} {
		assertEffectiveCap(t, ctx, env, svc, want)
	}
}
func assertEffectiveCap(t *testing.T, ctx context.Context, env *sourcesEnv, svc *sourcethroughput.Service, want int) {
	t.Helper()
	patch := sourcethroughput.Patch{DownloadConcurrency: sourcethroughput.Set(want)}
	if want == sourcesCap {
		patch.DownloadConcurrency = sourcethroughput.Clear[int]()
	}
	if _, err := svc.Update(ctx, 7537715367149829912, patch); err != nil {
		t.Fatal(err)
	}
	rec := env.get(t, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var rows []handler.SourceStatusDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%+v", rows)
	}
	if rows[0].SourceKey != "Comix" || rows[0].ActiveCount != 1 || rows[0].Cap != want || rows[1].Cap != sourcesCap {
		t.Fatalf("rows=%+v want cap=%d", rows, want)
	}
}

func TestSources_EffectivePersistedSourceCap_TrimsProviderIdentity(t *testing.T) {
	for _, tc := range []struct{ name, provider, key string }{
		{" Comix ", "7537715367149829912", "Comix"},
		{"\t\u00a0Comix\u2003\n", "7537715367149829912", "Comix"},
		{"", "\t\u00a07537715367149829912\u2003\n", "7537715367149829912"},
	} {
		t.Run(tc.key+tc.name, func(t *testing.T) {
			env := newSourcesEnv(t)
			ctx := context.Background()
			s := env.client.Series.Create().SetTitle("Padded Comix").SetSlug("padded-comix").SaveX(ctx)
			sp := env.client.SeriesProvider.Create().SetSeries(s).SetProvider(tc.provider).SetProviderName(tc.name).SetImportance(10).SaveX(ctx)
			env.client.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey("1").SetURL("https://example.test/1").SaveX(ctx)
			env.client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetState(entchapter.StateDownloading).SaveX(ctx)
			env.client.SourceThroughputPolicy.Create().SetSourceID(7537715367149829912).SetDownloadConcurrency(3).SaveX(ctx)
			rec := env.get(t, true)
			var rows []handler.SourceStatusDTO
			if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].SourceKey != tc.key || rows[0].Cap != 3 {
				t.Fatalf("rows=%+v, want canonical %q cap3", rows, tc.key)
			}
		})
	}
}
