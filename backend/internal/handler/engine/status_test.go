package engine_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/engine"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

// statusEnv wires an Echo instance with GET /api/engine/topology-status behind
// RequireOwner (so the 401 proof hits the real middleware) over a fresh testdb
// client, plus a valid owner Bearer token.
type statusEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
}

func newStatusEnv(t *testing.T) *statusEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	h := handler.NewHandler(apkcache.New(t.TempDir()), client)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/engine/topology-status", h.TopologyStatus)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &statusEnv{e: e, client: client, token: token}
}

func (env *statusEnv) get(t *testing.T, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/engine/topology-status", nil)
	if withAuth {
		r.Header.Set("Authorization", "Bearer "+env.token)
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// TestTopologyStatus_Unauthorized proves the route is behind RequireOwner.
func TestTopologyStatus_Unauthorized(t *testing.T) {
	env := newStatusEnv(t)
	rec := env.get(t, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", rec.Code)
	}
}

// TestTopologyStatus_EmptyIsZeroedResponse proves an empty DB is a valid 200 with
// every count zeroed and gaps an empty (non-null) array.
func TestTopologyStatus_EmptyIsZeroedResponse(t *testing.T) {
	env := newStatusEnv(t)
	rec := env.get(t, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	// gaps must serialize as [] not null.
	if got := rec.Body.String(); !strings.Contains(got, `"gaps":[]`) {
		t.Errorf("body %s: want \"gaps\":[]", got)
	}

	var dto handler.TopologyStatusDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := handler.TopologyStatusDTO{Gaps: []string{}}
	if dto.Repos != want.Repos || dto.Extensions != want.Extensions ||
		dto.Sources != want.Sources || dto.URLs != want.URLs || len(dto.Gaps) != 0 {
		t.Errorf("empty DB DTO = %+v, want all-zero counts + empty gaps", dto)
	}
}

// TestTopologyStatus_CountsAndGaps proves the DTO reports the exact captured
// counts and derives the human-readable gap notes for every outstanding kind.
func TestTopologyStatus_CountsAndGaps(t *testing.T) {
	env := newStatusEnv(t)
	ctx := context.Background()
	c := env.client

	c.HarvestedRepo.Create().SetURL("https://repo.one/index.min.json").SaveX(ctx)

	c.HarvestedExtension.Create().SetPkgName("ext.a").SetApkCached(true).SaveX(ctx)
	c.HarvestedExtension.Create().SetPkgName("ext.b").SetApkCached(false).SaveX(ctx)
	c.HarvestedExtension.Create().SetPkgName("ext.c").SetApkCached(false).SaveX(ctx)

	c.SourcePreference.Create().SetSourceID(123).SetKey("lang").SetValue("en").SaveX(ctx)

	// Two live sources (123, 456); 123 is url-filled, 456 is empty-but-fillable.
	s1 := c.Series.Create().SetTitle("Solo Leveling").SetSlug("solo-leveling").SaveX(ctx)
	c.SeriesProvider.Create().SetSeries(s1).SetProvider("123").SetSuwayomiID(42).SetURL("https://a.test/m").SaveX(ctx)
	s2 := c.Series.Create().SetTitle("Omniscient Reader").SetSlug("omniscient-reader").SaveX(ctx)
	c.SeriesProvider.Create().SetSeries(s2).SetProvider("456").SetSuwayomiID(43).SaveX(ctx)

	rec := env.get(t, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var dto handler.TopologyStatusDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"repos", dto.Repos, 1},
		{"extensions.total", dto.Extensions.Total, 3},
		{"extensions.cached", dto.Extensions.Cached, 1},
		{"sources.total", dto.Sources.Total, 2},
		{"sources.prefsCaptured", dto.Sources.PrefsCaptured, 1},
		{"urls.filled", dto.URLs.Filled, 1},
		{"urls.remaining", dto.URLs.Remaining, 1},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}

	// Gaps: 2 extensions uncached, 1 url unresolved, 1 source without prefs.
	assertGaps(t, dto.Gaps,
		"2 extensions not cached",
		"1 provider urls unresolved",
		"1 sources without captured preferences",
	)
}

// assertGaps proves dto.Gaps holds exactly the wanted notes (order-independent).
func assertGaps(t *testing.T, got []string, want ...string) {
	t.Helper()
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	if len(got) != len(wantSet) {
		t.Fatalf("gaps = %v, want %d notes", got, len(wantSet))
	}
	for _, g := range got {
		if !wantSet[g] {
			t.Errorf("unexpected gap note %q", g)
		}
	}
}
