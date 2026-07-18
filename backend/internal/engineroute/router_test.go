package engineroute_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// TestRouter_NoRoutesDelegatesEverythingToDefault pins the byte-for-byte-default
// invariant at the Router: with no routes installed, every content call AND every
// management call goes to the default instance (the instance fake sees nothing).
func TestRouter_NoRoutesDelegatesEverythingToDefault(t *testing.T) {
	def := fake.New(
		fake.WithSearchResult(1, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "from-default"}}}),
	)
	router := engineroute.NewRouter(def)

	res, err := router.Search(context.Background(), 1, "q", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Manga) != 1 || res.Manga[0].URL != "from-default" {
		t.Fatalf("Search routed away from default: %+v", res)
	}
	if def.CallCount("Search") != 1 {
		t.Fatalf("default Search calls = %d, want 1", def.CallCount("Search"))
	}
}

// TestRouter_RoutesContentBySourceID proves a content call for a routed source
// hits ITS instance, while an unrouted source falls back to the default — the
// core routing guarantee.
func TestRouter_RoutesContentBySourceID(t *testing.T) {
	def := fake.New(
		fake.WithSearchResult(1, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "default-1"}}}),
		fake.WithSearchResult(2, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "default-2"}}}),
	)
	inst := fake.New(
		fake.WithSearchResult(1, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "instance-1"}}}),
	)
	router := engineroute.NewRouter(def)
	router.SetRoutes(map[int64]sourceengine.Client{1: inst})

	// Source 1 is routed to the instance.
	got1, err := router.Search(context.Background(), 1, "q", 1)
	if err != nil {
		t.Fatalf("Search(1): %v", err)
	}
	if got1.Manga[0].URL != "instance-1" {
		t.Fatalf("Search(1) = %q, want instance-1 (routed)", got1.Manga[0].URL)
	}
	if inst.CallCount("Search") != 1 || def.CallCount("Search") != 0 {
		t.Fatalf("routing miss: inst=%d def=%d", inst.CallCount("Search"), def.CallCount("Search"))
	}

	// Source 2 is NOT routed — falls back to the default.
	got2, err := router.Search(context.Background(), 2, "q", 1)
	if err != nil {
		t.Fatalf("Search(2): %v", err)
	}
	if got2.Manga[0].URL != "default-2" {
		t.Fatalf("Search(2) = %q, want default-2 (unrouted)", got2.Manga[0].URL)
	}
}

// TestRouter_ManagementCallsAlwaysDefault proves engine-global calls (Sources,
// SetSocks, ...) always target the default instance even when routes exist, so a
// per-profile instance never receives the authoritative registry/config pushes.
func TestRouter_ManagementCallsAlwaysDefault(t *testing.T) {
	def := fake.New(fake.WithSources([]sourceengine.Source{{ID: 1, Name: "S1"}}))
	inst := fake.New()
	router := engineroute.NewRouter(def)
	router.SetRoutes(map[int64]sourceengine.Client{1: inst})

	if _, err := router.Sources(context.Background()); err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if _, err := router.SetSocks(context.Background(), sourceengine.SocksPatch{}); err != nil {
		t.Fatalf("SetSocks: %v", err)
	}
	if def.CallCount("Sources") != 1 || def.CallCount("SetSocks") != 1 {
		t.Fatalf("management calls did not hit default: %d/%d", def.CallCount("Sources"), def.CallCount("SetSocks"))
	}
	if inst.CallCount("Sources") != 0 || inst.CallCount("SetSocks") != 0 {
		t.Fatalf("management calls leaked to instance: %d/%d", inst.CallCount("Sources"), inst.CallCount("SetSocks"))
	}
}

// TestRouter_SetRoutesClears proves passing an empty map reverts to the
// byte-for-byte-default state (a binding removed → source back on the default).
func TestRouter_SetRoutesClears(t *testing.T) {
	def := fake.New(fake.WithSearchResult(1, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "default"}}}))
	inst := fake.New(fake.WithSearchResult(1, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "instance"}}}))
	router := engineroute.NewRouter(def)

	router.SetRoutes(map[int64]sourceengine.Client{1: inst})
	router.SetRoutes(nil) // cleared

	got, err := router.Search(context.Background(), 1, "q", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got.Manga[0].URL != "default" {
		t.Fatalf("after clear, Search(1) = %q, want default", got.Manga[0].URL)
	}
}
