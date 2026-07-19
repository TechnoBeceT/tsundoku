package extensions_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	handler "github.com/technobecet/tsundoku/internal/handler/extensions"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// fakeCollapser is an in-memory extensions.ScanlatorCollapser: it records the
// source id it was asked to collapse and returns canned (seriesProcessed, merged,
// skipped) counts (or an error). It lets the handler tests prove the Slice-B
// migration is invoked on flip-ON (and NOT on flip-OFF) without a real library.
type fakeCollapser struct {
	called   []int64
	sp       int
	merged   int
	skipped  int
	collapse error
}

func (f *fakeCollapser) CollapseIgnoredScanlatorSource(_ context.Context, sourceID int64) (int, int, int, error) {
	f.called = append(f.called, sourceID)
	if f.collapse != nil {
		return 0, 0, 0, f.collapse
	}
	return f.sp, f.merged, f.skipped, nil
}

// --- Preferences group `ignoreScanlator` field -------------------------------

// TestPreferences_IgnoreScanlatorReflectsStore proves each group's
// `ignoreScanlator` flag mirrors the Tsundoku-side ignore-scanlator store: a
// flagged source reports true (so the FE seeds its toggle on), an untouched one
// reports false.
func TestPreferences_IgnoreScanlatorReflectsStore(t *testing.T) {
	fc := sourceenginefake.New(
		sourceenginefake.WithExtensions([]sourceengine.Extension{{
			PkgName: "pkg.multi",
			Sources: []sourceengine.Source{
				{ID: 10, Name: "Hive Scans", Lang: "en"},
				{ID: 11, Name: "Hive Scans", Lang: "id"},
			},
		}}),
		sourceenginefake.WithPreferences(10, nil),
		sourceenginefake.WithPreferences(11, nil),
	)
	env := newTestEnv(t, fc)
	env.store.ignore[11] = true // language "id" flagged ignore-scanlator

	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/pkg.multi/preferences", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Preferences: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SourcePreferencesBySourceDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	flagByID := map[string]bool{}
	for _, g := range got.Sources {
		flagByID[g.SourceID] = g.IgnoreScanlator
	}
	if flagByID["10"] {
		t.Errorf("source 10 must report ignoreScanlator=false, got %+v", got.Sources)
	}
	if !flagByID["11"] {
		t.Errorf("flagged source 11 must report ignoreScanlator=true, got %+v", got.Sources)
	}
}

// --- PATCH /api/sources/:sourceId/ignore-scanlator ---------------------------

// TestSetSourceIgnoreScanlator_RoundTrip proves the toggle flags then un-flags a
// source, returning the authoritative re-read state each time and persisting it
// in the store (a subsequent read reflects it).
func TestSetSourceIgnoreScanlator_RoundTrip(t *testing.T) {
	env := newTestEnv(t, prefsFake())

	// Flag source 42.
	rec := env.do(http.MethodPatch, "/api/sources/42/ignore-scanlator", `{"ignoreScanlator":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("flag: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SourceIgnoreScanlatorDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SourceID != "42" || !got.IgnoreScanlator {
		t.Fatalf("flag response: want {42, ignoreScanlator=true}, got %+v", got)
	}
	if !env.store.ignore[42] {
		t.Errorf("store not updated: source 42 should be flagged")
	}

	// Un-flag it.
	rec = env.do(http.MethodPatch, "/api/sources/42/ignore-scanlator", `{"ignoreScanlator":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unflag: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.IgnoreScanlator {
		t.Fatalf("unflag response: want ignoreScanlator=false, got %+v", got)
	}
	if env.store.ignore[42] {
		t.Errorf("store not updated: source 42 should be un-flagged")
	}
}

// TestSetSourceIgnoreScanlator_MissingField400 proves a body without the required
// `ignoreScanlator` field is a 400 (fail-closed — an absent field must not read
// as an un-flag).
func TestSetSourceIgnoreScanlator_MissingField400(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.do(http.MethodPatch, "/api/sources/42/ignore-scanlator", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing ignoreScanlator: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetSourceIgnoreScanlator_BadSourceID400 proves a non-numeric :sourceId is a 400.
func TestSetSourceIgnoreScanlator_BadSourceID400(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.do(http.MethodPatch, "/api/sources/not-a-number/ignore-scanlator", `{"ignoreScanlator":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad sourceId: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetSourceIgnoreScanlator_StoreError502 proves a store write failure is a
// 502, not a false 200.
func TestSetSourceIgnoreScanlator_StoreError502(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	env.store.ignSetErr = errors.New("db write failed")
	rec := env.do(http.MethodPatch, "/api/sources/42/ignore-scanlator", `{"ignoreScanlator":true}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("store error: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetSourceIgnoreScanlator_FlagOnRunsMigration proves flipping the flag ON
// with a collapser wired runs the Slice-B on-enable migration and returns its
// summary in the response (§16 — the owner sees what the toggle did).
func TestSetSourceIgnoreScanlator_FlagOnRunsMigration(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	fc := &fakeCollapser{sp: 2, merged: 3, skipped: 1}
	env.h.WithScanlatorCollapser(fc)

	rec := env.do(http.MethodPatch, "/api/sources/42/ignore-scanlator", `{"ignoreScanlator":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("flag on: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SourceIgnoreScanlatorDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(fc.called) != 1 || fc.called[0] != 42 {
		t.Fatalf("collapser calls = %v, want [42]", fc.called)
	}
	if got.Migration == nil {
		t.Fatal("response migration summary is nil, want the collapse counts")
	}
	if got.Migration.SeriesProcessed != 2 || got.Migration.Merged != 3 || got.Migration.Skipped != 1 {
		t.Fatalf("migration = %+v, want {seriesProcessed:2 merged:3 skipped:1}", *got.Migration)
	}
}

// TestSetSourceIgnoreScanlator_FlagOffSkipsMigration proves flipping the flag OFF
// never runs the migration (one-way — no un-merge) and returns no summary.
func TestSetSourceIgnoreScanlator_FlagOffSkipsMigration(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	env.store.ignore[42] = true // start flagged so we can turn it off
	fc := &fakeCollapser{merged: 9}
	env.h.WithScanlatorCollapser(fc)

	rec := env.do(http.MethodPatch, "/api/sources/42/ignore-scanlator", `{"ignoreScanlator":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("flag off: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SourceIgnoreScanlatorDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(fc.called) != 0 {
		t.Fatalf("collapser must NOT run on flag-off, got calls %v", fc.called)
	}
	if got.Migration != nil {
		t.Fatalf("flag-off must carry no migration summary, got %+v", *got.Migration)
	}
}

// TestSetSourceIgnoreScanlator_MigrationError502 proves a hard migration failure
// surfaces as a 502 (the flag is already persisted; re-toggling re-runs it).
func TestSetSourceIgnoreScanlator_MigrationError502(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	env.h.WithScanlatorCollapser(&fakeCollapser{collapse: errors.New("sweep failed")})

	rec := env.do(http.MethodPatch, "/api/sources/42/ignore-scanlator", `{"ignoreScanlator":true}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("migration error: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
	// The flag was still persisted before the migration ran.
	if !env.store.ignore[42] {
		t.Error("flag should be persisted even when the migration fails")
	}
}

// TestSetSourceIgnoreScanlator_NoCollapserNoMigration proves the default env (no
// collapser wired) keeps the apply-forward Slice-A behaviour: flip-ON succeeds
// with no migration summary.
func TestSetSourceIgnoreScanlator_NoCollapserNoMigration(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.do(http.MethodPatch, "/api/sources/42/ignore-scanlator", `{"ignoreScanlator":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("flag on (no collapser): want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SourceIgnoreScanlatorDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Migration != nil {
		t.Fatalf("no collapser wired must carry no migration summary, got %+v", *got.Migration)
	}
}

// TestSetSourceIgnoreScanlator_Unauthorized proves the route is behind RequireOwner.
func TestSetSourceIgnoreScanlator_Unauthorized(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.noAuth(http.MethodPatch, "/api/sources/42/ignore-scanlator", `{"ignoreScanlator":true}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", rec.Code)
	}
}
