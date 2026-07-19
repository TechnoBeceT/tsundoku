package library_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
)

// consolidatePath builds POST /api/series/{id}/providers/consolidate for a series id.
func consolidatePath(seriesID string) string {
	return fmt.Sprintf("/api/series/%s/providers/consolidate", seriesID)
}

// TestConsolidateProviders_RequireOwner proves the route is behind RequireOwner.
func TestConsolidateProviders_RequireOwner(t *testing.T) {
	env := newEnv(t)
	rec := env.doUnauth("POST", consolidatePath(uuid.NewString()), `{"providerIds":["`+uuid.NewString()+`"],"target":{"existingProviderId":"`+uuid.NewString()+`"}}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth = %d, want 401", rec.Code)
	}
}

// TestConsolidateProviders_Validation400 sweeps the fail-closed request-shape
// guards — all reject SYNCHRONOUSLY with 400 before anything is launched.
func TestConsolidateProviders_Validation400(t *testing.T) {
	env := newEnv(t)
	sid := uuid.NewString()
	pid := uuid.NewString()

	cases := []struct {
		name string
		body string
	}{
		{"bad series id", ""}, // handled below (path uses a non-uuid)
		{"invalid json", `{`},
		{"empty providerIds", `{"providerIds":[],"target":{"existingProviderId":"` + pid + `"}}`},
		{"malformed provider id", `{"providerIds":["nope"],"target":{"existingProviderId":"` + pid + `"}}`},
		{"neither target arm", `{"providerIds":["` + pid + `"],"target":{}}`},
		{"both target arms", `{"providerIds":["` + pid + `"],"target":{"existingProviderId":"` + uuid.NewString() + `","source":{"source":"1","url":"/m","importance":1}}}`},
		{"bad existing target id", `{"providerIds":["` + pid + `"],"target":{"existingProviderId":"nope"}}`},
		{"target in merge set", `{"providerIds":["` + pid + `"],"target":{"existingProviderId":"` + pid + `"}}`},
		{"source arm missing url", `{"providerIds":["` + pid + `"],"target":{"source":{"source":"1","importance":1}}}`},
		{"source arm importance < 1", `{"providerIds":["` + pid + `"],"target":{"source":{"source":"1","url":"/m","importance":0}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := consolidatePath(sid)
			body := tc.body
			if tc.name == "bad series id" {
				path = consolidatePath("not-a-uuid")
				body = `{"providerIds":["` + pid + `"],"target":{"existingProviderId":"` + uuid.NewString() + `"}}`
			}
			rec := env.do("POST", path, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: want 400, got %d (%s)", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestConsolidateProviders_Accepted proves a well-formed request returns 202
// {started:true} — the handler launches the detached consolidation and returns
// immediately. (The single-flight 409 path is covered deterministically at the
// service level, TestStartConsolidateProviders_SingleFlightGuard, via the
// in-package block seam this external handler test cannot reach.)
func TestConsolidateProviders_Accepted(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()

	// A series with a feed-bearing linked target + one disk provider to fold in.
	mangaID := env.client.Category.Query().Where(entcategory.Name("Manga")).OnlyX(ctx).ID
	ser := env.client.Series.Create().SetTitle("H").SetSlug("h").SetCategoryID(mangaID).SaveX(ctx)
	target := env.client.SeriesProvider.Create().
		SetSeriesID(ser.ID).SetProvider("1").SetProviderName("Real").SetImportance(30).SaveX(ctx)
	env.client.ProviderChapter.Create().SetSeriesProviderID(target.ID).SetChapterKey("1").SetNumber(1).SaveX(ctx)
	disk := env.client.SeriesProvider.Create().
		SetSeriesID(ser.ID).SetProvider("old.disk").SetImportance(1).SaveX(ctx)

	body := fmt.Sprintf(`{"providerIds":["%s"],"target":{"existingProviderId":"%s"}}`, disk.ID, target.ID)
	if rec := env.do("POST", consolidatePath(ser.ID.String()), body); rec.Code != http.StatusAccepted {
		t.Fatalf("consolidate = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
}
