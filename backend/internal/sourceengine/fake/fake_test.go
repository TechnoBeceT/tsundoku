package fake_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// TestNew_ImplementsClient is a compile-time-adjacent check proving *Client
// satisfies sourceengine.Client — every later slice's tests bind to this.
func TestNew_ImplementsClient(t *testing.T) {
	var _ sourceengine.Client = fake.New()
}

// TestWithSources_And_Health proves WithSources seeds both Sources() and the
// Health() sources count.
func TestWithSources_And_Health(t *testing.T) {
	sources := []sourceengine.Source{{ID: 1, Name: "MangaDex", Lang: "en"}}
	c := fake.New(fake.WithSources(sources))

	got, err := c.Sources(context.Background())
	if err != nil || len(got) != 1 || got[0] != sources[0] {
		t.Fatalf("Sources = %+v, %v", got, err)
	}
	health, err := c.Health(context.Background())
	if err != nil || health.Sources != 1 {
		t.Fatalf("Health = %+v, %v", health, err)
	}
}

// TestWithSearchResult_SharedAcrossSearchPopularLatest proves a single
// WithSearchResult configuration answers Search, Popular, and Latest for the
// same sourceID.
func TestWithSearchResult_SharedAcrossSearchPopularLatest(t *testing.T) {
	res := sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "/m/1", Title: "One Piece"}}, HasNextPage: true}
	c := fake.New(fake.WithSearchResult(7, res))

	for name, call := range map[string]func() (sourceengine.SearchResult, error){
		"Search":  func() (sourceengine.SearchResult, error) { return c.Search(context.Background(), 7, "q", 1) },
		"Popular": func() (sourceengine.SearchResult, error) { return c.Popular(context.Background(), 7, 1) },
		"Latest":  func() (sourceengine.SearchResult, error) { return c.Latest(context.Background(), 7, 1) },
	} {
		got, err := call()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got.Manga) != 1 || got.Manga[0].URL != "/m/1" || !got.HasNextPage {
			t.Errorf("%s = %+v, want %+v", name, got, res)
		}
	}
}

// TestWithMangaDetailsChaptersPages proves the (sourceID,url)-keyed options
// resolve independently of each other.
func TestWithMangaDetailsChaptersPages(t *testing.T) {
	details := sourceengine.MangaDetails{URL: "/m/1", Title: "One Piece"}
	chapters := []sourceengine.Chapter{{URL: "/m/1/ch/1", Name: "Chapter 1"}}
	pages := []sourceengine.Page{{Index: 0, URL: "/m/1/ch/1/page/0"}}
	c := fake.New(
		fake.WithMangaDetails(7, "/m/1", details),
		fake.WithChapters(7, "/m/1", chapters),
		fake.WithPages(7, "/m/1/ch/1", pages),
	)

	gotDetails, err := c.MangaDetails(context.Background(), 7, "/m/1")
	if err != nil || !reflect.DeepEqual(gotDetails, details) {
		t.Errorf("MangaDetails = %+v, %v, want %+v", gotDetails, err, details)
	}
	gotChapters, err := c.Chapters(context.Background(), 7, "/m/1")
	if err != nil || len(gotChapters) != 1 || gotChapters[0] != chapters[0] {
		t.Errorf("Chapters = %+v, %v, want %+v", gotChapters, err, chapters)
	}
	gotPages, err := c.Pages(context.Background(), 7, "/m/1/ch/1")
	if err != nil || len(gotPages) != 1 || gotPages[0] != pages[0] {
		t.Errorf("Pages = %+v, %v, want %+v", gotPages, err, pages)
	}
}

// TestWithImage_KeyedBySourceAndPageURL proves Image is resolved by the
// (sourceID, pageURL) pair, ignoring the imageURL argument (mirrors the real
// engine host, which resolves imageURL itself when absent).
func TestWithImage_KeyedBySourceAndPageURL(t *testing.T) {
	c := fake.New(fake.WithImage(7, "/m/1/ch/1/page/0", []byte{1, 2, 3}, "image/jpeg"))

	data, ct, err := c.Image(context.Background(), 7, "/m/1/ch/1/page/0", "https://anything")
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	if string(data) != "\x01\x02\x03" || ct != "image/jpeg" {
		t.Errorf("Image = %v %q, want [1 2 3] image/jpeg", data, ct)
	}
}

// TestWithPreferences_And_SetPreferences proves SetPreferences applies
// changes by Key and returns the updated list, leaving the seeded slice
// untouched (the fake must not alias the caller's backing array).
func TestWithPreferences_And_SetPreferences(t *testing.T) {
	seed := []sourceengine.Preference{{Key: "useSourceLang", Type: "SwitchPreferenceCompat", CurrentValue: false}}
	c := fake.New(fake.WithPreferences(7, seed))

	got, err := c.Preferences(context.Background(), 7)
	if err != nil || len(got) != 1 || got[0].CurrentValue != false {
		t.Fatalf("Preferences = %+v, %v", got, err)
	}

	updated, err := c.SetPreferences(context.Background(), 7, map[string]any{"useSourceLang": true})
	if err != nil {
		t.Fatalf("SetPreferences: %v", err)
	}
	if len(updated) != 1 || updated[0].CurrentValue != true {
		t.Fatalf("SetPreferences result = %+v, want CurrentValue=true", updated)
	}
	if seed[0].CurrentValue != false {
		t.Errorf("SetPreferences mutated the caller's seed slice: %+v", seed)
	}
	if c.CallCount("SetPreferences") != 1 {
		t.Errorf("SetPreferences call count = %d, want 1", c.CallCount("SetPreferences"))
	}
}

// TestWithExtensions_InstallAndUninstall_ToggleIsInstalled proves Install/
// UninstallExtension toggle IsInstalled on the matching pkgName only, leaving
// every other entry untouched.
func TestWithExtensions_InstallAndUninstall_ToggleIsInstalled(t *testing.T) {
	extensions := []sourceengine.Extension{
		{PkgName: "pkg.a", Name: "A", IsInstalled: false},
		{PkgName: "pkg.b", Name: "B", IsInstalled: true},
	}
	c := fake.New(fake.WithExtensions(extensions))

	afterInstall, err := c.InstallExtension(context.Background(), "pkg.a", "")
	if err != nil {
		t.Fatalf("InstallExtension: %v", err)
	}
	if !afterInstall[0].IsInstalled || !afterInstall[1].IsInstalled {
		t.Fatalf("InstallExtension result = %+v, want both installed", afterInstall)
	}

	afterUninstall, err := c.UninstallExtension(context.Background(), "pkg.b")
	if err != nil {
		t.Fatalf("UninstallExtension: %v", err)
	}
	if !afterUninstall[0].IsInstalled || afterUninstall[1].IsInstalled {
		t.Fatalf("UninstallExtension result = %+v, want only pkg.a installed", afterUninstall)
	}
}

// TestWithExtensions_RefreshUpdateList_ReturnTheFullList proves
// RefreshExtensions/UpdateExtension/Extensions all answer the full,
// unfiltered extension list (the fake has no repo or version to actually
// change).
func TestWithExtensions_RefreshUpdateList_ReturnTheFullList(t *testing.T) {
	extensions := []sourceengine.Extension{
		{PkgName: "pkg.a", Name: "A"},
		{PkgName: "pkg.b", Name: "B"},
	}
	c := fake.New(fake.WithExtensions(extensions))

	refreshed, err := c.RefreshExtensions(context.Background())
	assertExtensionCount(t, "RefreshExtensions", refreshed, err)

	updated, err := c.UpdateExtension(context.Background(), "pkg.a")
	assertExtensionCount(t, "UpdateExtension", updated, err)

	listed, err := c.Extensions(context.Background())
	assertExtensionCount(t, "Extensions", listed, err)
}

// assertExtensionCount is a shared test helper asserting a 2-element,
// error-free extension list result.
func assertExtensionCount(t *testing.T, method string, got []sourceengine.Extension, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	if len(got) != 2 {
		t.Fatalf("%s result = %+v, want 2 entries", method, got)
	}
}

// TestRepos_ReadIsDefensivelyCopied proves mutating a slice returned by
// Repos never leaks back into the fake's internal state.
func TestRepos_ReadIsDefensivelyCopied(t *testing.T) {
	c := fake.New(fake.WithRepos([]string{"https://a"}))

	got, err := c.Repos(context.Background())
	if err != nil || len(got) != 1 || got[0] != "https://a" {
		t.Fatalf("Repos = %+v, %v", got, err)
	}
	got[0] = "mutated"

	again, err := c.Repos(context.Background())
	if err != nil || len(again) != 1 || again[0] != "https://a" {
		t.Fatalf("Repos after mutating a previous read = %+v, %v (fake must copy defensively)", again, err)
	}
}

// TestSetRepos_ReplacesTheWholeList proves SetRepos replaces the configured
// list wholesale and the replacement is visible to a subsequent Repos read.
func TestSetRepos_ReplacesTheWholeList(t *testing.T) {
	c := fake.New(fake.WithRepos([]string{"https://a"}))

	fresh, err := c.SetRepos(context.Background(), []string{"https://b", "https://c"})
	if err != nil {
		t.Fatalf("SetRepos: %v", err)
	}
	if len(fresh) != 2 || fresh[0] != "https://b" || fresh[1] != "https://c" {
		t.Fatalf("SetRepos result = %+v", fresh)
	}
	again, err := c.Repos(context.Background())
	if err != nil || len(again) != 2 || again[0] != "https://b" {
		t.Fatalf("Repos after SetRepos = %+v, %v", again, err)
	}
}

// TestSetFlareSolverr_And_SetSocks_ApplyPartial proves the config setters
// apply only the patch's non-nil fields, leaving the rest of the stored
// config untouched.
func TestSetFlareSolverr_And_SetSocks_ApplyPartial(t *testing.T) {
	c := fake.New()

	enabled := true
	url := "http://flare:8191"
	got, err := c.SetFlareSolverr(context.Background(), sourceengine.FlareSolverrPatch{Enabled: &enabled, URL: &url})
	if err != nil {
		t.Fatalf("SetFlareSolverr: %v", err)
	}
	if !got.Enabled || got.URL != url {
		t.Fatalf("SetFlareSolverr result = %+v", got)
	}

	session := "sess"
	ttl := 15
	timeout := 60
	fallback := true
	got2, err := c.SetFlareSolverr(context.Background(), sourceengine.FlareSolverrPatch{
		Session: &session, SessionTTL: &ttl, Timeout: &timeout, AsResponseFallback: &fallback,
	})
	if err != nil {
		t.Fatalf("SetFlareSolverr: %v", err)
	}
	want := sourceengine.FlareSolverrConfig{
		Enabled: true, URL: url, Session: session, SessionTTL: ttl, Timeout: timeout, AsResponseFallback: fallback,
	}
	if got2 != want {
		t.Fatalf("SetFlareSolverr second patch = %+v, want %+v (earlier fields must survive)", got2, want)
	}

	socksEnabled := true
	version := 5
	host := "127.0.0.1"
	port := "1080"
	username := "user"
	password := "pass"
	gotSocks, err := c.SetSocks(context.Background(), sourceengine.SocksPatch{
		Enabled: &socksEnabled, Version: &version, Host: &host, Port: &port, Username: &username, Password: &password,
	})
	if err != nil {
		t.Fatalf("SetSocks: %v", err)
	}
	wantSocks := sourceengine.SocksConfig{Enabled: true, Version: 5, Host: host, Port: port, Username: username, Password: password}
	if gotSocks != wantSocks {
		t.Fatalf("SetSocks result = %+v, want %+v", gotSocks, wantSocks)
	}
}

// TestWithError_ForcesTheNamedMethodToFail proves WithError makes exactly the
// named method return the given error, leaving every other method unaffected.
func TestWithError_ForcesTheNamedMethodToFail(t *testing.T) {
	wantErr := errors.New("boom")
	c := fake.New(fake.WithSources(nil), fake.WithError("Sources", wantErr))

	if _, err := c.Sources(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Sources error = %v, want %v", err, wantErr)
	}
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health: want nil error (WithError only targets Sources), got %v", err)
	}
	if c.CallCount("Sources") != 1 {
		t.Errorf("Sources call count = %d, want 1 (a forced error must still be recorded)", c.CallCount("Sources"))
	}
}

// TestWithError_CoversEveryMethod drives WithError across EVERY Client
// method by its exact exported name, proving the error-check branch in each
// one actually short-circuits before returning a result. One table, one
// assertion shape — new Client methods must be added here too.
func TestWithError_CoversEveryMethod(t *testing.T) {
	wantErr := errors.New("boom")
	ctx := context.Background()

	calls := map[string]func(c *fake.Client) error{
		"Health":             func(c *fake.Client) error { _, err := c.Health(ctx); return err },
		"Search":             func(c *fake.Client) error { _, err := c.Search(ctx, 1, "q", 1); return err },
		"Popular":            func(c *fake.Client) error { _, err := c.Popular(ctx, 1, 1); return err },
		"Latest":             func(c *fake.Client) error { _, err := c.Latest(ctx, 1, 1); return err },
		"MangaDetails":       func(c *fake.Client) error { _, err := c.MangaDetails(ctx, 1, "/m"); return err },
		"Chapters":           func(c *fake.Client) error { _, err := c.Chapters(ctx, 1, "/m"); return err },
		"Pages":              func(c *fake.Client) error { _, err := c.Pages(ctx, 1, "/m/ch/1"); return err },
		"Image":              func(c *fake.Client) error { _, _, err := c.Image(ctx, 1, "/p", ""); return err },
		"Sources":            func(c *fake.Client) error { _, err := c.Sources(ctx); return err },
		"Preferences":        func(c *fake.Client) error { _, err := c.Preferences(ctx, 1); return err },
		"SetPreferences":     func(c *fake.Client) error { _, err := c.SetPreferences(ctx, 1, nil); return err },
		"Extensions":         func(c *fake.Client) error { _, err := c.Extensions(ctx); return err },
		"InstallExtension":   func(c *fake.Client) error { _, err := c.InstallExtension(ctx, "pkg", ""); return err },
		"RefreshExtensions":  func(c *fake.Client) error { _, err := c.RefreshExtensions(ctx); return err },
		"UpdateExtension":    func(c *fake.Client) error { _, err := c.UpdateExtension(ctx, "pkg"); return err },
		"UninstallExtension": func(c *fake.Client) error { _, err := c.UninstallExtension(ctx, "pkg"); return err },
		"Repos":              func(c *fake.Client) error { _, err := c.Repos(ctx); return err },
		"SetRepos":           func(c *fake.Client) error { _, err := c.SetRepos(ctx, nil); return err },
		"SetFlareSolverr": func(c *fake.Client) error {
			_, err := c.SetFlareSolverr(ctx, sourceengine.FlareSolverrPatch{})
			return err
		},
		"SetSocks": func(c *fake.Client) error { _, err := c.SetSocks(ctx, sourceengine.SocksPatch{}); return err },
	}

	for method, call := range calls {
		t.Run(method, func(t *testing.T) {
			c := fake.New(fake.WithError(method, wantErr))
			if err := call(c); !errors.Is(err, wantErr) {
				t.Fatalf("%s error = %v, want %v", method, err, wantErr)
			}
			if c.CallCount(method) != 1 {
				t.Errorf("%s call count = %d, want 1", method, c.CallCount(method))
			}
		})
	}
}
