//go:build suwayomi

// Package suwayomi_test — real-Suwayomi end-to-end integration tests.
//
// Build tag: suwayomi. Run with:
//
//	go test -tags suwayomi -v -timeout 15m ./internal/suwayomi/...
//
// These tests require:
//   - Docker (for the ephemeral PostgreSQL container via testdb)
//   - Java 21+ (auto-detected from /usr/lib/jvm/*/bin/java)
//   - Network access (to download the Suwayomi v2.2.2100 JAR on first run)
//
// The Suwayomi instance is shared across all tests in this file via the
// testharness singleton. It is launched once per test-binary run and torn
// down via t.Cleanup at run end.
//
// # GraphQL shape validation (Task 4 items)
//
// Three items from client.go were flagged in Task 4 for live validation:
//  1. Chapter filter operator: chapters(filter:{mangaId:{equalTo:N}})
//  2. fetchChapterPages page-URL format (relative path vs. absolute URL).
//  3. LongString scalar acceptance for sourceId in fetchSourceManga.
//
// TestValidateGraphQLShapes (below) covers all three explicitly so the test
// output documents whether the assumptions held or were corrected.
package suwayomi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for the Postgres-boot verification query
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
	"github.com/technobecet/tsundoku/internal/suwayomi/testharness"
)

// TestMain launches the shared Suwayomi instance before any test runs and
// tears it down after m.Run() returns. This ensures the server lifetime spans
// all tests in the binary, regardless of which test calls Shared(t) first.
//
// IMPORTANT: when unit tests re-exec this binary as a helper subprocess (the
// TestHelperProcess pattern in helpers_test.go), GO_SUWAYOMI_TEST_HELPER is
// set. In that case we must skip the real Suwayomi setup so the subprocess
// does not try to start a second Suwayomi (which would fail due to port
// conflict) and instead just runs TestHelperProcess.
func TestMain(m *testing.M) {
	// Detect helper-subprocess invocations (see helpers_test.go). If this
	// binary was re-executed by helperCmd, skip the Suwayomi setup entirely.
	if os.Getenv("GO_SUWAYOMI_TEST_HELPER") != "" {
		os.Exit(m.Run())
	}

	// Find a Java 21+ JVM. If none is found, skip the whole binary gracefully.
	javaPath, err := testharness.FindJava21()
	if err != nil {
		fmt.Fprintf(os.Stderr, "suwayomi harness: skipping all tests — %v\n", err)
		os.Exit(0) // skip, not failure
	}

	if err := testharness.Setup(javaPath); err != nil {
		fmt.Fprintf(os.Stderr, "suwayomi harness: setup failed — %v\n", err)
		testharness.GlobalCleanup()
		os.Exit(1)
	}

	code := m.Run()
	testharness.GlobalCleanup()
	os.Exit(code)
}

// TestShape1_LongString_SourceID validates Task-4 shape assumption 1:
// sourceId is typed as LongString! in the Suwayomi GraphQL schema.
// A schema-type error on Search would mean the assumption is wrong.
func TestShape1_LongString_SourceID(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	results, err := inst.Client().Search(ctx, suwayomi.LocalSourceID, testharness.FixtureMangaTitle)
	if err != nil {
		t.Fatalf("Search with sourceId=%q: %v\n(check: is LongString! the correct scalar for sourceId?)", suwayomi.LocalSourceID, err)
	}
	t.Logf("CONFIRMED: LongString! scalar accepted for sourceId; got %d results", len(results))
}

// TestShape2_ChapterFilter_EqualTo validates Task-4 shape assumption 2:
// chapters are queried with filter:{mangaId:{equalTo:N}}.
//
// CORRECTION discovered by Task-7 live validation: chapters are not cached by
// Suwayomi until fetchChapters is called. We must call FetchChapters first,
// then verify that the chapters(filter:{mangaId:{equalTo:N}}) query returns the
// same data. Both the fetchChapters mutation and the chapters query are validated.
func TestShape2_ChapterFilter_EqualTo(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()
	results, err := client.Search(ctx, suwayomi.LocalSourceID, testharness.FixtureMangaTitle)
	if err != nil {
		t.Skipf("Search failed (skipping shape2): %v", err)
	}
	if len(results) == 0 {
		t.Skip("no search results (local source may not have indexed; skipping shape2)")
	}

	mangaID := results[0].ID

	// FetchChapters (mutation) populates Suwayomi's cache from the source.
	fetched, err := client.FetchChapters(ctx, mangaID)
	if err != nil {
		t.Fatalf("FetchChapters(mangaId=%d): %v", mangaID, err)
	}
	t.Logf("CONFIRMED: fetchChapters mutation returned %d chapters for mangaId=%d", len(fetched), mangaID)

	// MangaChapters (query) reads from the now-populated cache.
	cached, err := client.MangaChapters(ctx, mangaID)
	if err != nil {
		t.Fatalf("MangaChapters(mangaId=%d): %v\n(check: is equalTo the correct filter operator?)", mangaID, err)
	}
	t.Logf("CONFIRMED: chapters(filter:{mangaId:{equalTo:%d}}) returned %d chapters", mangaID, len(cached))
}

// TestShape3_ChapterPages_URLFormat validates Task-4 shape assumption 3:
// fetchChapterPages returns relative path URLs (e.g. /api/v1/manga/N/chapter/M/page/K).
func TestShape3_ChapterPages_URLFormat(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()

	results, err := client.Search(ctx, suwayomi.LocalSourceID, testharness.FixtureMangaTitle)
	if err != nil || len(results) == 0 {
		t.Skipf("Search failed or empty (skipping shape3): err=%v results=%d", err, len(results))
	}

	// FetchChapters populates the chapter cache before querying pages.
	chapters, err := client.FetchChapters(ctx, results[0].ID)
	if err != nil || len(chapters) == 0 {
		t.Skipf("FetchChapters failed or empty (skipping shape3): err=%v len=%d", err, len(chapters))
	}

	pages, err := client.ChapterPages(ctx, chapters[0].ID)
	if err != nil {
		t.Fatalf("ChapterPages(chapterID=%d): %v", chapters[0].ID, err)
	}
	if len(pages) == 0 {
		t.Fatalf("fetchChapterPages returned zero pages for chapter %d", chapters[0].ID)
	}

	first := pages[0]
	logPageURLFormat(t, first)
}

// logPageURLFormat logs whether the page URL is absolute or relative.
func logPageURLFormat(t *testing.T, pageURL string) {
	t.Helper()
	switch {
	case strings.HasPrefix(pageURL, "http://") || strings.HasPrefix(pageURL, "https://"):
		t.Logf("CORRECTION: page URLs are ABSOLUTE (not relative as assumed in Task 4); PageBytes uses them directly: %q", pageURL)
	case strings.HasPrefix(pageURL, "/"):
		t.Logf("CONFIRMED: page URLs are RELATIVE paths (as assumed in Task 4); PageBytes prepends BaseURL: %q", pageURL)
	default:
		t.Logf("NOTE: unexpected page URL format: %q", pageURL)
	}
}

// TestShape4_BrowseEnumType validates the discover-browse shape assumption:
// the GraphQL enum TYPE NAME `FetchSourceMangaType` is correct.
//
// Why this needs a real Suwayomi: the existing searchMutation hardcodes
// `type: SEARCH` as an inline literal, so the type name was never declared as a
// typed variable before. Browse introduces `$type: FetchSourceMangaType!` (see
// browseMutation in client.go) — the FIRST time that enum type name crosses the
// wire. The httptest fakes in client_test.go echo back canned JSON and so cannot
// catch a wrong type name or a server-side schema rejection. Only a real
// Suwayomi parses the GraphQL document and validates the declared variable type
// against its schema.
//
// What this confirms: calling Browse(...BrowsePopular...) against the real
// fixture returns NO error. A wrong `FetchSourceMangaType` (or a wrong enum
// VALUE like POPULAR) would surface as a GraphQL schema/validation error from
// doGraphQL — exactly the failure mode this test exists to catch. A zero-manga
// result is acceptable: the point is that the enum/type name is accepted and
// hasNextPage parses (BrowseResult decodes cleanly). We additionally exercise
// BrowseLatest (LATEST reuses the same FetchSourceMangaType), but tolerate a
// source-capability error there — the Local source may not support a "latest"
// listing, and that is NOT a schema/type-name rejection.
func TestShape4_BrowseEnumType(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()

	// POPULAR is the load-bearing assertion: it must be accepted as a value of
	// FetchSourceMangaType. Any error here means the enum type name or value is
	// wrong and must fail the test.
	popular, err := client.Browse(ctx, suwayomi.LocalSourceID, suwayomi.BrowsePopular, 1)
	if err != nil {
		t.Fatalf("Browse(sourceId=%q, type=POPULAR, page=1): %v\n(check: is FetchSourceMangaType! the correct enum type name, and POPULAR a valid value?)", suwayomi.LocalSourceID, err)
	}
	t.Logf("CONFIRMED: FetchSourceMangaType! accepted with value POPULAR; got %d mangas, hasNextPage=%v", len(popular.Mangas), popular.HasNextPage)

	// LATEST reuses the same FetchSourceMangaType. The Local source may not
	// support a latest listing — tolerate a source-capability error, but still
	// log the outcome so the test record shows whether LATEST round-tripped.
	latest, err := client.Browse(ctx, suwayomi.LocalSourceID, suwayomi.BrowseLatest, 1)
	if err != nil {
		t.Logf("NOTE: Browse(type=LATEST) returned an error — tolerated as a possible source-capability limitation (NOT a type-name rejection): %v", err)
	} else {
		t.Logf("CONFIRMED: FetchSourceMangaType! accepted with value LATEST; got %d mangas, hasNextPage=%v", len(latest.Mangas), latest.HasNextPage)
	}
}

// TestShape7_MangaMetadataFields is the MERGE GATE for the M4 rich-hover-preview
// feature: it proves, against a real Suwayomi, that the `author`, `artist`,
// `genre`, and `description` MangaType field names added to mangaFieldSelection
// (client.go) are accepted by the schema on all three operations that share it —
// Search, Browse, and MangaMeta.
//
// Why this needs a real Suwayomi: the httptest fakes in client_test.go only
// prove the Go struct DECODES whatever JSON is handed to it — they cannot catch
// a wrong GraphQL field NAME, which the server would reject with a schema
// validation error before ever returning data. Only a real Suwayomi validates
// the selection set against MangaType.
//
// What this confirms: Search/Browse/MangaMeta all return NO error with the
// widened selection. The Local source's fixture manga may not itself carry
// author/artist/genre/description (a local worktree source rarely does) — a
// nil/empty value is fine and expected; the load-bearing assertion is the
// ABSENCE of a GraphQL error, which is what a bad field name would produce.
func TestShape7_MangaMetadataFields(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()

	results, err := client.Search(ctx, suwayomi.LocalSourceID, testharness.FixtureMangaTitle)
	if err != nil {
		t.Fatalf("Search (widened selection incl. author/artist/genre/description): %v\n(check: are these MangaType field names correct?)", err)
	}
	if len(results) == 0 {
		t.Skip("no search results (local source may not have indexed; skipping shape7)")
	}
	m := results[0]
	t.Logf("CONFIRMED: Search accepted author/artist/genre/description; author=%v artist=%v genre=%v description=%v",
		m.Author, m.Artist, m.Genre, m.Description)

	popular, err := client.Browse(ctx, suwayomi.LocalSourceID, suwayomi.BrowsePopular, 1)
	if err != nil {
		t.Fatalf("Browse (widened selection incl. author/artist/genre/description): %v", err)
	}
	t.Logf("CONFIRMED: Browse accepted author/artist/genre/description; got %d mangas", len(popular.Mangas))

	meta, err := client.MangaMeta(ctx, m.ID)
	if err != nil {
		t.Fatalf("MangaMeta(mangaId=%d) (widened selection incl. author/artist/genre/description): %v", m.ID, err)
	}
	t.Logf("CONFIRMED: MangaMeta accepted author/artist/genre/description; author=%v artist=%v genre=%v description=%v",
		meta.Author, meta.Artist, meta.Genre, meta.Description)
}

// TestShape8_FetchMangaDetails is the MERGE GATE for the on-demand
// Discover-hover-details feature: it proves, against a real Suwayomi, that the
// `fetchManga(input:{id:$id})` MUTATION (client.go's fetchMangaMutation) is
// accepted by the schema and returns a `manga` payload with no GraphQL error.
//
// This is deliberately a DIFFERENT operation from TestShape7's MangaMeta
// check: MangaMeta reads the manga(id) QUERY, which never contacts the source
// (see its doc comment) — TestShape7 could pass even if the source never
// populated author/artist/genre/description. fetchManga is the MUTATION that
// forces Suwayomi to re-fetch details from the source; this test's
// load-bearing assertion is that the mutation shape (input field `id`, result
// field `manga`) is correct, not that the Local source's fixture manga
// happens to carry real author/artist data (a local worktree source rarely
// does — see TestShape7's identical caveat).
func TestShape8_FetchMangaDetails(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()

	results, err := client.Search(ctx, suwayomi.LocalSourceID, testharness.FixtureMangaTitle)
	if err != nil {
		t.Fatalf("Search (to obtain a mangaId for fetchManga): %v", err)
	}
	if len(results) == 0 {
		t.Skip("no search results (local source may not have indexed; skipping shape8)")
	}
	m := results[0]

	details, err := client.FetchMangaDetails(ctx, m.ID)
	if err != nil {
		t.Fatalf("FetchMangaDetails(mangaId=%d) (fetchManga mutation): %v\n(check: is the mutation name/input field/result field correct?)", m.ID, err)
	}
	t.Logf("CONFIRMED: fetchManga(input:{id}) accepted with no GraphQL error; title=%q author=%v artist=%v genre=%v description=%v",
		details.Title, details.Author, details.Artist, details.Genre, details.Description)
}

// TestShape5_ServerSettings is the MERGE GATE for the Suwayomi settings-proxy.
// It proves, against a real Suwayomi, that:
//
//  1. the `settings` query + the FlareSolverr/SOCKS field NAMES are correct
//     (ServerSettings decodes with no schema/type error), and
//  2. the `setSettings` mutation + PartialSettingsTypeInput shape are correct,
//     including the partial-input no-clobber contract and socksProxyPort being a
//     String on the wire (SetServerSettings sends "1081" and it round-trips).
//
// It captures the original values, applies a distinctive partial patch, reads it
// back, asserts the round-trip, then RESTORES the original values so the shared
// harness is left unchanged for other tests. No enabled flags route real traffic
// during the test window (the Local source reads from disk, not the network), so
// toggling the values is side-effect-free here.
func TestShape5_ServerSettings(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()

	before, err := client.ServerSettings(ctx)
	if err != nil {
		t.Fatalf("ServerSettings (query shape): %v\n(check: is `settings` the query and are the FlareSolverr/SOCKS field names correct?)", err)
	}
	t.Logf("CONFIRMED: settings query decoded; before=%+v", before)

	// Distinctive values, including a numeric STRING port (proves socksProxyPort
	// is a String! on the wire) and a flipped bool (proves Boolean round-trip).
	wantURL := "http://shape5.test:8191"
	wantTimeout := 42
	wantVersion := 4
	wantHost := "shape5-host"
	wantPort := "1081"
	wantFallback := !before.FlareSolverrAsResponseFallback

	patch := suwayomi.SuwayomiSettingsPatch{
		FlareSolverrURL:                strptr(wantURL),
		FlareSolverrTimeout:            intptr(wantTimeout),
		FlareSolverrAsResponseFallback: boolptr(wantFallback),
		SocksProxyVersion:              intptr(wantVersion),
		SocksProxyHost:                 strptr(wantHost),
		SocksProxyPort:                 strptr(wantPort),
	}
	if err := client.SetServerSettings(ctx, patch); err != nil {
		t.Fatalf("SetServerSettings (mutation shape): %v\n(check: is setSettings(input:{settings:PartialSettingsTypeInput!}) correct and socksProxyPort a String?)", err)
	}

	after, err := client.ServerSettings(ctx)
	if err != nil {
		t.Fatalf("ServerSettings read-back: %v", err)
	}

	// Restore the original values regardless of assertion outcome.
	t.Cleanup(func() {
		restore := suwayomi.SuwayomiSettingsPatch{
			FlareSolverrURL:                strptr(before.FlareSolverrURL),
			FlareSolverrTimeout:            intptr(before.FlareSolverrTimeout),
			FlareSolverrAsResponseFallback: boolptr(before.FlareSolverrAsResponseFallback),
			SocksProxyVersion:              intptr(before.SocksProxyVersion),
			SocksProxyHost:                 strptr(before.SocksProxyHost),
			SocksProxyPort:                 strptr(before.SocksProxyPort),
		}
		if err := client.SetServerSettings(context.Background(), restore); err != nil {
			t.Logf("WARN: failed to restore original Suwayomi settings: %v", err)
		}
	})

	assertSettingEq(t, "flareSolverrUrl", after.FlareSolverrURL, wantURL)
	assertSettingEq(t, "flareSolverrTimeout", after.FlareSolverrTimeout, wantTimeout)
	assertSettingEq(t, "flareSolverrAsResponseFallback", after.FlareSolverrAsResponseFallback, wantFallback)
	assertSettingEq(t, "socksProxyVersion", after.SocksProxyVersion, wantVersion)
	assertSettingEq(t, "socksProxyHost", after.SocksProxyHost, wantHost)
	assertSettingEq(t, "socksProxyPort (String! on the wire)", after.SocksProxyPort, wantPort)
	t.Logf("CONFIRMED: setSettings partial update round-tripped; after=%+v", after)
}

// assertSettingEq fails the test with a named message when got != want.
func assertSettingEq[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

// strptr / intptr / boolptr are pointer helpers for building a partial patch.
func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }
func boolptr(b bool) *bool    { return &b }

// TestShape6_Extensions is the MERGE GATE for the Suwayomi extension-management
// proxy. It proves, against a real Suwayomi, the GraphQL shapes that httptest
// fakes cannot (the fakes echo canned JSON; only a real server validates the
// document against its schema):
//
// Tier 1 (MUST pass; local harness only, no external network):
//  1. Extensions(ctx) decodes with NO schema/type error — proving the
//     `extensions { nodes { … } }` query AND every ExtensionType field name +
//     casing (pkgName, isInstalled, isObsolete, hasUpdate, …). A zero-length
//     list is acceptable (the harness configures no repos).
//  2. FetchExtensions(ctx) with input:{} is accepted (no schema error); an
//     empty list is tolerated.
//  3. ExtensionRepos read → SetExtensionRepos(one URL) → ExtensionRepos read-back
//     asserts the round-trip, then a t.Cleanup RESTORES the original list. This
//     is the strongest live proof here, since the harness has no installable
//     extensions.
//
// Tier 2 (BEST-EFFORT; needs network + a real repo; NEVER fails the gate on its
// absence): point a real repo, fetchExtensions, and IF an installable extension
// appears, install → re-read → assert isInstalled==true → uninstall → assert it
// flips back. Guarded by a short network probe; any network/repo/APK
// unavailability calls t.Skip. updateExtension's input shape is already
// introspection-confirmed, so Tier 2 is bonus live proof, not the gate.
//
// Tier 2 also confirms the M1 icon-proxy bugfix's live discovery: every fetched
// extension's IconURL is Suwayomi's own REST icon path
// "/api/v1/extension/icon/{apkFileName}.apk" (NOT a full URL), and that path is
// genuinely fetchable via PageBytes — proving handler/extensions.Icon's
// "look up by pkgName, stream that entry's own IconURL via PageBytes" design
// actually reaches real image bytes, not just a plausible-looking string.
func TestShape6_Extensions(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client := inst.Client()

	// --- Tier 1.1: extensions query decodes (field names + casing) -----------
	exts, err := client.Extensions(ctx)
	if err != nil {
		t.Fatalf("Extensions (query shape): %v\n(check: is `extensions { nodes { … } }` correct and are the ExtensionType field names — incl. isInstalled/isObsolete/hasUpdate/pkgName — right?)", err)
	}
	t.Logf("CONFIRMED: extensions query decoded; %d extension(s)", len(exts))

	// --- Tier 1.2: fetchExtensions(input:{}) accepted ------------------------
	fetched, err := client.FetchExtensions(ctx)
	if err != nil {
		t.Fatalf("FetchExtensions (mutation shape, input:{}): %v\n(check: is `fetchExtensions(input:{}) { extensions { … } }` correct?)", err)
	}
	t.Logf("CONFIRMED: fetchExtensions(input:{}) accepted; %d extension(s)", len(fetched))

	// --- Tier 1.3: extensionRepos read/write round-trip + restore ------------
	before, err := client.ExtensionRepos(ctx)
	if err != nil {
		t.Fatalf("ExtensionRepos (read shape): %v\n(check: is `settings { extensionRepos }` correct?)", err)
	}
	t.Logf("CONFIRMED: extensionRepos read; before=%v", before)

	// Restore the original repo list regardless of outcome (and after any Tier 2
	// changes), so the shared harness is left unchanged for other tests.
	t.Cleanup(func() {
		if err := client.SetExtensionRepos(context.Background(), before); err != nil {
			t.Logf("WARN: failed to restore original extension repos: %v", err)
		}
	})

	// Suwayomi server-side validates the repo URL FORMAT (a github-raw-style regex)
	// before storing — but storing does NOT fetch the URL, so a format-valid but
	// never-fetched placeholder proves the read/write wire shape network-free.
	const testRepo = "https://raw.githubusercontent.com/tsundoku-shape-test/extensions/repo/index.min.json"
	if err := client.SetExtensionRepos(ctx, []string{testRepo}); err != nil {
		t.Fatalf("SetExtensionRepos (write shape): %v\n(check: is setSettings(input:{settings:{extensionRepos:[String!]}}) correct?)", err)
	}
	after, err := client.ExtensionRepos(ctx)
	if err != nil {
		t.Fatalf("ExtensionRepos read-back: %v", err)
	}
	if len(after) != 1 || after[0] != testRepo {
		t.Fatalf("extensionRepos round-trip: got %v, want [%s]", after, testRepo)
	}
	t.Logf("CONFIRMED: extensionRepos write round-tripped; after=%v", after)

	// --- Tier 2: best-effort live install/uninstall --------------------------
	tier2Extensions(t, client)
}

// tier2Extensions is the best-effort live install/uninstall round-trip. It never
// fails the gate on network/repo/APK unavailability — it t.Skips. It only asserts
// (and can fail) AFTER a successful install, where a non-flipping isInstalled
// would be a genuine bug.
func tier2Extensions(t *testing.T, client suwayomi.Client) {
	t.Helper()
	const realRepo = "https://raw.githubusercontent.com/keiyoushi/extensions/repo/index.min.json"
	if !probeURL(realRepo) {
		t.Skip("Tier 2 skipped: no network access to a real extensions repo")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := client.SetExtensionRepos(ctx, []string{realRepo}); err != nil {
		t.Skipf("Tier 2 skipped: SetExtensionRepos(real repo): %v", err)
	}
	fetched, err := client.FetchExtensions(ctx)
	if err != nil {
		t.Skipf("Tier 2 skipped: FetchExtensions(real repo): %v", err)
	}

	tier2IconURLShape(t, ctx, client, fetched)

	var target string
	for _, e := range fetched {
		if !e.IsInstalled && !e.IsObsolete {
			target = e.PkgName
			break
		}
	}
	if target == "" {
		t.Skip("Tier 2 skipped: no installable extension found in the repo listing")
	}

	tier2InstallUninstallRoundTrip(t, ctx, client, target)
}

// tier2IconURLShape confirms the M1 icon-proxy discovery against a real repo
// listing: the first extension's IconURL matches Suwayomi's own REST icon path
// shape ("/api/v1/extension/icon/{apkFileName}.apk", confirmed live 2026-07-03
// against Suwayomi v2.2.2100 via the keiyoushi repo), and that path is
// genuinely fetchable via PageBytes (proving handler/extensions.Icon's
// PageBytes(e.IconURL) call reaches real bytes, not just a plausible string).
// It never fails the gate on network/repo unavailability — only on a shape or
// fetch regression once a listing was already obtained.
func tier2IconURLShape(t *testing.T, ctx context.Context, client suwayomi.Client, fetched []suwayomi.Extension) {
	t.Helper()
	if len(fetched) == 0 {
		t.Skip("Tier 2 icon shape skipped: repo listing was empty")
	}
	icon := fetched[0].IconURL
	if !strings.HasPrefix(icon, "/api/v1/extension/icon/") || !strings.HasSuffix(icon, ".apk") {
		t.Fatalf("IconURL shape regression: got %q, want \"/api/v1/extension/icon/{apkFileName}.apk\"", icon)
	}
	t.Logf("CONFIRMED: IconURL shape = %q", icon)

	data, ext, err := client.PageBytes(ctx, icon)
	if err != nil {
		t.Fatalf("PageBytes(%q) failed — the confirmed icon path is no longer fetchable: %v", icon, err)
	}
	if len(data) == 0 {
		t.Error("PageBytes returned zero bytes for a confirmed icon path")
	}
	t.Logf("CONFIRMED: icon fetched via PageBytes, %d bytes, ext=%q", len(data), ext)
}

// tier2InstallUninstallRoundTrip performs the live install → assert isInstalled →
// uninstall → assert not-installed round-trip for an already-resolved target
// package. It t.Skips on an install/uninstall transport failure (network/APK
// unavailability) and only t.Errors on a non-flipping isInstalled — the one
// genuine bug this tier can catch. Extracted from tier2Extensions to keep both
// functions within the cyclop complexity budget.
func tier2InstallUninstallRoundTrip(t *testing.T, ctx context.Context, client suwayomi.Client, target string) {
	t.Helper()

	if err := client.SetExtensionState(ctx, target, suwayomi.ExtensionInstall); err != nil {
		t.Skipf("Tier 2 skipped: install %q failed (likely network/APK fetch): %v", target, err)
	}
	// Always attempt an uninstall so the harness is left clean.
	t.Cleanup(func() {
		_ = client.SetExtensionState(context.Background(), target, suwayomi.ExtensionUninstall)
	})

	if installed := findExtension(t, client, target); !installed.IsInstalled {
		t.Errorf("after install, %q isInstalled=false (expected true)", target)
	} else {
		t.Logf("CONFIRMED: install set isInstalled=true for %q", target)
	}

	if err := client.SetExtensionState(ctx, target, suwayomi.ExtensionUninstall); err != nil {
		t.Skipf("Tier 2 partial: uninstall %q failed: %v", target, err)
	}
	if uninstalled := findExtension(t, client, target); uninstalled.IsInstalled {
		t.Errorf("after uninstall, %q isInstalled=true (expected false)", target)
	} else {
		t.Logf("CONFIRMED: uninstall flipped isInstalled back to false for %q", target)
	}
}

// probeURL does a short HEAD probe to decide whether the network/repo is
// reachable for the best-effort Tier 2 path. A <500 status counts as reachable.
func probeURL(rawURL string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 500
}

// findExtension re-reads the extension list and returns the entry for pkgName,
// failing the test if it is absent (used after a state change to assert the
// re-read reflects the new isInstalled value).
func findExtension(t *testing.T, client suwayomi.Client, pkgName string) suwayomi.Extension {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	exts, err := client.Extensions(ctx)
	if err != nil {
		t.Fatalf("Extensions (find %q): %v", pkgName, err)
	}
	for _, e := range exts {
		if e.PkgName == pkgName {
			return e
		}
	}
	t.Fatalf("extension %q not found in list after state change", pkgName)
	return suwayomi.Extension{}
}

// TestShape8_SourcePreferences is the MERGE GATE for the per-source preferences
// (M3 "Configure") proxy. It proves, against a real Suwayomi, the GraphQL shapes
// that httptest fakes cannot — a fake echoes canned JSON, but only a real server
// validates the Preference-union selection (crucially the per-fragment
// currentValue/default ALIASES that avoid the FieldsConflict rejection) and the
// updateSourcePreference input against its schema.
//
// Tier 1 (MUST pass; local harness only, no external network):
//   - SourcePreferences(LocalSourceID) decodes with NO schema/type error —
//     proving `source(id){ preferences { …aliased union fragments… } }` is a
//     valid, FieldsConflict-free document. A zero-length list is acceptable (the
//     Local source may expose no preferences).
//
// Tier 2 (BEST-EFFORT; needs network + a real repo; NEVER fails the gate on its
// absence): point the keiyoushi repo, install an extension, resolve its sources
// via ExtensionSources(pkgName), read that source's preferences, flip a boolean
// (Switch/CheckBox) preference via SetSourcePreference, assert the returned list
// reflects the flip, then restore + uninstall. This is the only place the write
// mutation + the "exactly one *State field" mapping + ExtensionSources are proven
// live; their shapes are otherwise introspection-confirmed, so Tier 2 is bonus.
func TestShape8_SourcePreferences(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client := inst.Client()

	// --- Tier 1: the union query decodes (aliases avoid FieldsConflict) -------
	prefs, err := client.SourcePreferences(ctx, suwayomi.LocalSourceID)
	if err != nil {
		t.Fatalf("SourcePreferences (union query shape): %v\n(check: is `source(id){preferences{…}}` correct and are currentValue/default aliased per fragment to avoid FieldsConflict?)", err)
	}
	t.Logf("CONFIRMED: source preferences query decoded; %d preference(s) on the Local source", len(prefs))

	// --- Tier 2: best-effort live write round-trip ---------------------------
	tier2SourcePreferences(t, client)
}

// tier2SourcePreferences is the best-effort live install → ExtensionSources →
// read → write-flip → restore round-trip. It t.Skips on any network/repo/APK
// unavailability and only t.Errors on a genuine bug (a flipped boolean not
// reflected in the returned list). Extracted to keep the test within the cyclop
// budget.
func tier2SourcePreferences(t *testing.T, client suwayomi.Client) {
	t.Helper()
	const realRepo = "https://raw.githubusercontent.com/keiyoushi/extensions/repo/index.min.json"
	if !probeURL(realRepo) {
		t.Skip("Tier 2 skipped: no network access to a real extensions repo")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := client.SetExtensionRepos(ctx, []string{realRepo}); err != nil {
		t.Skipf("Tier 2 skipped: SetExtensionRepos(real repo): %v", err)
	}
	t.Cleanup(func() { _ = client.SetExtensionRepos(context.Background(), nil) })

	fetched, err := client.FetchExtensions(ctx)
	if err != nil {
		t.Skipf("Tier 2 skipped: FetchExtensions(real repo): %v", err)
	}

	var pkg string
	for _, e := range fetched {
		if !e.IsInstalled && !e.IsObsolete {
			pkg = e.PkgName
			break
		}
	}
	if pkg == "" {
		t.Skip("Tier 2 skipped: no installable extension found in the repo listing")
	}

	if err := client.SetExtensionState(ctx, pkg, suwayomi.ExtensionInstall); err != nil {
		t.Skipf("Tier 2 skipped: install %q failed (likely network/APK fetch): %v", pkg, err)
	}
	t.Cleanup(func() { _ = client.SetExtensionState(context.Background(), pkg, suwayomi.ExtensionUninstall) })

	sources, err := client.ExtensionSources(ctx, pkg)
	if err != nil {
		t.Fatalf("ExtensionSources(%q): %v\n(check: is `extension(pkgName){source{nodes{…}}}` correct?)", pkg, err)
	}
	if len(sources) == 0 {
		t.Skipf("Tier 2 skipped: extension %q reported no sources", pkg)
	}
	t.Logf("CONFIRMED: ExtensionSources(%q) returned %d source(s)", pkg, len(sources))

	tier2WriteFlip(t, client, sources[0].ID)
}

// tier2WriteFlip finds the first boolean (Switch/CheckBox) preference on sourceID,
// flips it via SetSourcePreference, asserts the returned refreshed list reflects
// the new value, then restores it. It t.Skips when the source has no boolean
// preference (nothing safe to flip); it only t.Errors on a non-reflecting write.
func tier2WriteFlip(t *testing.T, client suwayomi.Client, sourceID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	prefs, err := client.SourcePreferences(ctx, sourceID)
	if err != nil {
		t.Fatalf("SourcePreferences(%q): %v", sourceID, err)
	}

	idx := -1
	for i, p := range prefs {
		if (p.Type == suwayomi.PreferenceSwitch || p.Type == suwayomi.PreferenceCheckBox) && p.CurrentBool != nil {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.Skip("Tier 2 write skipped: source has no boolean preference to flip safely")
	}

	target := prefs[idx]
	orig := *target.CurrentBool
	want := !orig
	value := suwayomi.BoolPreferenceValue(target.Type, want)

	refreshed, err := client.SetSourcePreference(ctx, sourceID, target.Position, value)
	if err != nil {
		t.Fatalf("SetSourcePreference(pos=%d): %v", target.Position, err)
	}
	// Restore regardless of assertion outcome.
	t.Cleanup(func() {
		_, _ = client.SetSourcePreference(context.Background(), sourceID, target.Position,
			suwayomi.BoolPreferenceValue(target.Type, orig))
	})

	if target.Position >= len(refreshed) {
		t.Fatalf("refreshed list shorter than the written position (%d >= %d)", target.Position, len(refreshed))
	}
	got := refreshed[target.Position].CurrentBool
	if got == nil || *got != want {
		t.Errorf("after write, preference %q current=%v, want %v", target.Key, got, want)
	} else {
		t.Logf("CONFIRMED: SetSourcePreference flipped %q %v→%v (reflected in the returned list)", target.Key, orig, want)
	}
}

// TestShape9_SourceMeta is the MERGE GATE for the per-language source
// enable/disable feature. Suwayomi has NO server-side "disabled source"
// concept — enable/disable is a CLIENT convention over generic per-source
// metadata (SourceType.meta), the same convention Suwayomi-WebUI itself uses.
// A fake HTTP transport can echo canned JSON for this shape, but only a real
// server validates that `meta { key value }` is a legal SourceType selection
// and that `setSourceMeta` is a real mutation accepting the documented input.
//
// Tier 1 (MUST pass; local harness only, no external network), entirely
// against Suwayomi's built-in Local source so no extension install is needed:
//
//  1. Sources() decodes with NO schema/type error — proving
//     `sources { nodes { … meta { key value } } }` is a valid selection —
//     and the Local source resolves as enabled (no isEnabled meta key has
//     ever been written for it, so "absent ⇒ enabled" is exercised for real).
//  2. SetSourceEnabled(LocalSourceID, false) round-trips: a fresh Sources()
//     read reports the Local source as disabled — proving
//     `setSourceMeta(input:{meta:{sourceId,key:"isEnabled",value:"false"}})`
//     is accepted and its effect is visible on the very next read (Suwayomi
//     applies the write synchronously, no cache lag).
//  3. SetSourceEnabled(LocalSourceID, true) restores it (re-enable sets
//     "true" EXPLICITLY, per the owner-ratified design — never deletes the
//     meta row) and a final Sources() read confirms it is enabled again.
//
// ExtensionSources carries the identical `meta { key value }` selection (see
// source_preferences.go) — proven by construction, not re-tested here, since
// it is the same SourceType field via a different root query.
func TestShape9_SourceMeta(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()

	// --- (a) the meta selection decodes; Local source defaults to enabled ----
	before, err := findSourceByID(ctx, client, suwayomi.LocalSourceID)
	if err != nil {
		t.Fatalf("Sources (meta selection shape): %v\n(check: is `sources{nodes{meta{key value}}}` a legal SourceType selection?)", err)
	}
	if before.Disabled {
		t.Fatalf("Local source reported Disabled=true before any write — expected absent isEnabled meta to default to enabled")
	}
	t.Logf("CONFIRMED: sources query decoded meta{key value}; Local source defaults to enabled")

	// Always attempt to restore the Local source to enabled, regardless of
	// assertion outcome below, so a failing run doesn't poison later tests
	// sharing the same harness instance.
	t.Cleanup(func() {
		_ = client.SetSourceEnabled(context.Background(), suwayomi.LocalSourceID, true)
	})

	// --- (b) setSourceMeta round-trips: disable then re-read -----------------
	if err := client.SetSourceEnabled(ctx, suwayomi.LocalSourceID, false); err != nil {
		t.Fatalf("SetSourceEnabled(false): %v\n(check: is `setSourceMeta(input:{meta:{sourceId,key,value}})` the correct mutation/input shape?)", err)
	}
	disabled, err := findSourceByID(ctx, client, suwayomi.LocalSourceID)
	if err != nil {
		t.Fatalf("Sources after disable: %v", err)
	}
	if !disabled.Disabled {
		t.Fatalf("after SetSourceEnabled(false), Sources() still reports the source as enabled")
	}
	t.Logf("CONFIRMED: setSourceMeta(isEnabled=false) round-trips through a fresh Sources() read")

	// --- (c) re-enable sets "true" explicitly, restoring the default state ---
	if err := client.SetSourceEnabled(ctx, suwayomi.LocalSourceID, true); err != nil {
		t.Fatalf("SetSourceEnabled(true): %v", err)
	}
	restored, err := findSourceByID(ctx, client, suwayomi.LocalSourceID)
	if err != nil {
		t.Fatalf("Sources after re-enable: %v", err)
	}
	if restored.Disabled {
		t.Fatalf("after SetSourceEnabled(true), Sources() still reports the source as disabled")
	}
	t.Logf("CONFIRMED: setSourceMeta(isEnabled=true) round-trips; re-enable is an explicit write, not a meta-row delete")
}

// findSourceByID returns the Source with the given id from a fresh
// client.Sources() call, or an error if the id is absent from the list.
func findSourceByID(ctx context.Context, client suwayomi.Client, id string) (suwayomi.Source, error) {
	sources, err := client.Sources(ctx)
	if err != nil {
		return suwayomi.Source{}, err
	}
	for _, s := range sources {
		if s.ID == id {
			return s, nil
		}
	}
	return suwayomi.Source{}, fmt.Errorf("source %q not found in Sources() list", id)
}

// TestShape10_ChapterScanlator is the MERGE GATE for the scanlator-aware-
// providers feature: it proves, against a real Suwayomi, that the `scanlator`
// field added to the shared chapter selection (client.go's fetchChaptersMutation
// and chaptersQuery) is a legal ChapterType selection — i.e. the field NAME is
// correct and the server does not reject the query/mutation document.
//
// Why this needs a real Suwayomi: the httptest fakes in client_test.go only
// prove the Go struct DECODES whatever JSON is handed to it — they cannot
// catch a wrong GraphQL field name, which the server would reject with a
// schema validation error before ever returning data. Only a real Suwayomi
// validates the selection set against ChapterType.
//
// What this confirms: FetchChapters (mutation) and MangaChapters (query) both
// return NO error with `scanlator` in the selection. The Local source's
// fixture chapters may not themselves carry a scanlator value (a local
// worktree source is not an aggregator like Comix) — a "" value is fine and
// expected; the load-bearing assertion is the ABSENCE of a GraphQL error,
// which is what a wrong field name would produce (mirrors TestShape7/8's
// identical caveat).
func TestShape10_ChapterScanlator(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()

	results, err := client.Search(ctx, suwayomi.LocalSourceID, testharness.FixtureMangaTitle)
	if err != nil {
		t.Fatalf("Search (to obtain a mangaId for the chapter selections): %v", err)
	}
	if len(results) == 0 {
		t.Skip("no search results (local source may not have indexed; skipping shape10)")
	}
	mangaID := results[0].ID

	fetched, err := client.FetchChapters(ctx, mangaID)
	if err != nil {
		t.Fatalf("FetchChapters(mangaId=%d) (selection incl. scanlator): %v\n(check: is `scanlator` a valid ChapterType field name?)", mangaID, err)
	}
	t.Logf("CONFIRMED: fetchChapters accepted the scanlator selection; got %d chapters", len(fetched))
	if len(fetched) > 0 {
		t.Logf("scanlator on chapters[0] = %q", fetched[0].Scanlator)
	}

	cached, err := client.MangaChapters(ctx, mangaID)
	if err != nil {
		t.Fatalf("MangaChapters(mangaId=%d) (selection incl. scanlator): %v\n(check: is `scanlator` a valid ChapterType field name?)", mangaID, err)
	}
	t.Logf("CONFIRMED: chapters query accepted the scanlator selection; got %d chapters", len(cached))
	if len(cached) > 0 {
		t.Logf("scanlator on cached[0] = %q", cached[0].Scanlator)
	}
}

// TestShape11_SourcePreferences is a discovery-first probe run AHEAD of a
// planned client method for reading a source's per-source LOGIN preferences
// (username/password/base-url/quality fields exposed by aggregator sources).
// It independently re-confirms — via live GraphQL introspection, not just a
// round-trip read — the three things that planned method must trust:
//
//  1. The Preference UNION's member type names. __type(name:"Preference")
//     .possibleTypes is asked directly (the schema's own source of truth),
//     rather than assuming the five names already hard-coded in
//     source_preferences.go's PreferenceType constants are still correct.
//  2. currentValue is the wire field carrying the PERSISTED value — proven
//     both by a live SourcePreferences() read (which decodes into exactly a
//     {key, type, currentValue}-shaped SourcePreference) and by introspecting
//     EditTextPreference's field list for that name.
//  3. A PASSWORD-style EditText preference (the shape a source login field
//     uses) is NEVER masked: introspecting EditTextPreference's field set
//     proves the type carries no isPassword/masked/secure/inputType field at
//     all — there is nowhere in the schema for Suwayomi to even RECORD that a
//     preference is a password field, so currentValue is unconditionally the
//     plain persisted string for every EditTextPreference, credential or not.
//
// This does not duplicate TestShape8_SourcePreferences (the merge gate for
// the already-shipped source_preferences.go client, which proves the
// read/write ROUND TRIP): TestShape8's Tier 2 write-flip only ever exercises a
// boolean (Switch/CheckBox) preference, so the password/EditText masking
// question was never actually probed live. This test closes that gap at the
// SCHEMA level — a definitive answer that holds regardless of which
// extension's login field is inspected, since it examines the TYPE, not one
// specific source's data.
func TestShape11_SourcePreferences(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()

	// --- (a) live read decodes into the {key, type, currentValue} shape ------
	prefs, err := client.SourcePreferences(ctx, suwayomi.LocalSourceID)
	if err != nil {
		t.Fatalf("SourcePreferences (union read shape): %v\n(check: is `source(id){preferences{…}}` still correct?)", err)
	}
	t.Logf("CONFIRMED: SourcePreferences decoded %d preference(s) on the Local source", len(prefs))
	for _, p := range prefs {
		t.Logf("  key=%q type=%s currentValue(bool)=%v currentValue(string)=%v",
			p.Key, p.Type, p.CurrentBool, p.CurrentString)
	}

	// --- (b) introspect the Preference union's member type names -------------
	memberTypes := introspectPossibleTypes(t, ctx, inst.BaseURL(), "Preference")
	wantMembers := []string{
		"CheckBoxPreference", "SwitchPreference", "EditTextPreference",
		"ListPreference", "MultiSelectListPreference",
	}
	for _, want := range wantMembers {
		if !containsStr(memberTypes, want) {
			t.Errorf("Preference union missing expected member type %q; got %v", want, memberTypes)
		}
	}
	t.Logf("CONFIRMED: Preference union member types = %v", memberTypes)

	// --- (c) introspect EditTextPreference: currentValue exists, no masking --
	assertEditTextPreferenceShape(t, ctx, inst.BaseURL())
}

// assertEditTextPreferenceShape introspects EditTextPreference's field set and
// asserts (1) currentValue is present — the field the planned client method
// will read a login value from — and (2) NO masking-flavoured field name is
// present, which is the live proof that a password-style login preference is
// never masked at the schema level (there is nothing to hold a mask flag).
// Extracted from TestShape11 to keep that test's cyclomatic complexity low.
func assertEditTextPreferenceShape(t *testing.T, ctx context.Context, baseURL string) {
	t.Helper()

	fields := introspectFieldNames(t, ctx, baseURL, "EditTextPreference")
	t.Logf("CONFIRMED: EditTextPreference fields = %v", fields)

	if !containsStr(fields, "currentValue") {
		t.Fatalf("EditTextPreference has no currentValue field — the assumed current-value field name is wrong")
	}

	// Candidate names a masking/secure-input flag would plausibly use. None of
	// these should exist; if one ever appears, the planned client method must
	// be updated to honour it before exposing password fields verbatim.
	maskingCandidates := []string{"isPassword", "password", "masked", "isMasked", "secure", "inputType", "obscured"}
	for _, candidate := range maskingCandidates {
		if containsStr(fields, candidate) {
			t.Errorf("EditTextPreference unexpectedly carries a masking-flavoured field %q — "+
				"a password preference may be maskable; the planned reader must respect it, not assume plaintext", candidate)
		}
	}
	t.Logf("CONFIRMED: EditTextPreference carries NO password/masking field — currentValue is " +
		"always the plain persisted string, so a source login/password field returns UNMASKED")
}

// introspectionQuery is a generic GraphQL __type introspection document. It
// asks the SERVER directly for a named type's union membership and field
// list — the schema's own source of truth, independent of any assumption
// baked into the Go client's hand-written selections.
const introspectionQuery = `
query Introspect($name: String!) {
  __type(name: $name) {
    name
    kind
    possibleTypes { name }
    fields { name }
  }
}`

// introspectionResult is the decode target for introspectionQuery.
type introspectionResult struct {
	Type struct {
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		PossibleTypes []struct {
			Name string `json:"name"`
		} `json:"possibleTypes"`
		Fields []struct {
			Name string `json:"name"`
		} `json:"fields"`
	} `json:"__type"`
}

// rawIntrospect POSTs the introspectionQuery for typeName directly to
// baseURL+"/api/graphql" and decodes its data field. It bypasses the Go
// Client entirely on purpose: introspection is a one-off discovery tool for
// THIS probe, not a runtime capability any production code needs, so it gets
// no Client method of its own (mirrors doGraphQL's request shape in
// client.go without duplicating any exported surface).
func rawIntrospect(t *testing.T, ctx context.Context, baseURL, typeName string) introspectionResult {
	t.Helper()

	reqBody, err := json.Marshal(map[string]any{
		"query":     introspectionQuery,
		"variables": map[string]any{"name": typeName},
	})
	if err != nil {
		t.Fatalf("marshal introspection request for %q: %v", typeName, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/graphql", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("build introspection request for %q: %v", typeName, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("introspection request for %q: %v", typeName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("introspection HTTP %d for %q: %s", resp.StatusCode, typeName, strings.TrimSpace(string(b)))
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode introspection envelope for %q: %v", typeName, err)
	}
	if len(envelope.Errors) > 0 {
		msgs := make([]string, len(envelope.Errors))
		for i, e := range envelope.Errors {
			msgs[i] = e.Message
		}
		t.Fatalf("introspection GraphQL errors for %q: %s", typeName, strings.Join(msgs, "; "))
	}

	var result introspectionResult
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		t.Fatalf("decode introspection data for %q: %v", typeName, err)
	}
	return result
}

// introspectPossibleTypes returns the possibleTypes name list for a GraphQL
// union/interface type (e.g. "Preference"), via a live __type query.
func introspectPossibleTypes(t *testing.T, ctx context.Context, baseURL, typeName string) []string {
	t.Helper()
	result := rawIntrospect(t, ctx, baseURL, typeName)
	out := make([]string, len(result.Type.PossibleTypes))
	for i, p := range result.Type.PossibleTypes {
		out[i] = p.Name
	}
	return out
}

// introspectFieldNames returns the field-name list for a GraphQL object type
// (e.g. "EditTextPreference"), via the same live __type query.
func introspectFieldNames(t *testing.T, ctx context.Context, baseURL, typeName string) []string {
	t.Helper()
	result := rawIntrospect(t, ctx, baseURL, typeName)
	out := make([]string, len(result.Type.Fields))
	for i, f := range result.Type.Fields {
		out[i] = f.Name
	}
	return out
}

// containsStr reports whether s is present in list.
func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestE2E_AddSeriesDispatchDownload is the Milestone 2 end-to-end proof:
//
//	real Suwayomi (local source) →
//	Ingest.Search finds fixture manga →
//	Ingest.AddSeries populates rows (state=wanted) →
//	download.Dispatcher with real suwayomi.Fetcher →
//	RunOnce →
//	each chapter reaches state=downloaded with a Komga-valid CBZ on disk.
//
// It also validates faithful provenance (provider, page_count) on the chapter rows.
func TestE2E_AddSeriesDispatchDownload(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	client := inst.Client()

	// ── Ephemeral database ────────────────────────────────────────────────────
	db := testdb.New(t)

	// ── Temp storage dir for CBZ output ──────────────────────────────────────
	storageDir := t.TempDir()

	// ── Step 1: Search the local source for the fixture manga ─────────────────
	t.Log("Step 1: searching local source for fixture manga...")
	var mangaID int
	var mangaTitle string

	// Retry search up to 30 s because the Local source may need time to index
	// on first launch.
	if err := retryUntil(ctx, 30*time.Second, func() error {
		results, err := client.Search(ctx, suwayomi.LocalSourceID, testharness.FixtureMangaTitle)
		if err != nil {
			return err
		}
		for _, m := range results {
			if strings.Contains(m.Title, testharness.FixtureMangaTitle) {
				mangaID = m.ID
				mangaTitle = m.Title
				return nil
			}
		}
		return fmt.Errorf("fixture manga %q not found in search results (got %d)", testharness.FixtureMangaTitle, len(results))
	}); err != nil {
		t.Fatalf("Step 1 — search: %v", err)
	}
	t.Logf("Step 1: found fixture manga ID=%d title=%q", mangaID, mangaTitle)

	// ── Step 2: AddSeries — populate DB rows ──────────────────────────────────
	t.Log("Step 2: AddSeries — ingesting chapters into DB...")
	ingest := suwayomi.NewIngest(client, db)
	result, err := ingest.AddSeries(ctx, "local", mangaID, mangaTitle, "")
	if err != nil {
		t.Fatalf("Step 2 — AddSeries: %v", err)
	}
	t.Logf("Step 2: ingest result: new_chapters=%d new_provider_chapters=%d", result.NewChapters, result.NewProviderChapters)

	if result.NewChapters != testharness.FixtureChapterCount {
		t.Errorf("Step 2: expected %d new chapters, got %d", testharness.FixtureChapterCount, result.NewChapters)
	}

	// ── Step 3: Verify all chapters are in state=wanted ───────────────────────
	t.Log("Step 3: verifying chapters are in state=wanted...")
	wanted, err := chapter.WantedChapters(ctx, db, 100)
	if err != nil {
		t.Fatalf("Step 3 — WantedChapters: %v", err)
	}
	if len(wanted) != testharness.FixtureChapterCount {
		t.Fatalf("Step 3: expected %d wanted chapters, got %d", testharness.FixtureChapterCount, len(wanted))
	}
	t.Logf("Step 3: %d chapters in state=wanted — OK", len(wanted))

	// ── Step 4: Run the dispatcher with the real Suwayomi fetcher ─────────────
	t.Log("Step 4: running dispatcher with real suwayomi.Fetcher...")
	hub := sse.NewHub()
	fetcher := suwayomi.NewFetcher(client)
	dispatcher := download.New(db, fetcher, hub, download.Config{
		Storage: storageDir,
	}, settings.Static{Retries: 1, Backoff: 0}, nil)

	if _, err := dispatcher.RunOnce(ctx); err != nil {
		t.Fatalf("Step 4 — RunOnce: %v", err)
	}
	t.Log("Step 4: RunOnce completed")

	// ── Step 5: Assert downloaded chapters + Komga-valid CBZs ─────────────────
	t.Log("Step 5: asserting downloaded chapters and CBZ validity...")
	// mangaTitle is the title returned by Search (= testharness.FixtureMangaTitle).
	// It must appear in ComicInfo.Series for Komga series grouping.
	assertDownloadedCBZs(t, storageDir, testharness.FixtureChapterCount, mangaTitle)

	// ── Step 6: Verify provenance in the DB (state=downloaded, page_count) ────
	t.Log("Step 6: verifying DB provenance...")
	assertProvenance(t, ctx, db, testharness.FixtureChapterCount)
}

// assertDownloadedCBZs walks storageDir, counts .cbz files, and validates each
// one is Komga-valid (ComicInfo.xml present + at least one image page +
// ComicInfo.Series equals wantSeriesTitle so Komga can group the series).
func assertDownloadedCBZs(t *testing.T, storageDir string, expectedCount int, wantSeriesTitle string) {
	t.Helper()

	var cbzFiles []string
	if err := filepath.Walk(storageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.EqualFold(filepath.Ext(path), ".cbz") {
			cbzFiles = append(cbzFiles, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk storageDir: %v", err)
	}

	if len(cbzFiles) != expectedCount {
		t.Errorf("expected %d CBZ files, found %d in %s", expectedCount, len(cbzFiles), storageDir)
		for _, f := range cbzFiles {
			t.Logf("  found: %s", f)
		}
		if len(cbzFiles) == 0 {
			return
		}
	}

	for _, cbzPath := range cbzFiles {
		cbzPath := cbzPath // loop var capture
		t.Run(filepath.Base(cbzPath), func(t *testing.T) {
			assertKomgaValidCBZ(t, cbzPath, wantSeriesTitle)
		})
	}
}

// assertKomgaValidCBZ opens a CBZ and checks:
//  1. ComicInfo.xml is present and parseable by disk.ReadComicInfoFromCBZ.
//  2. At least one image page is present (png/jpg/webp/gif/avif).
//  3. ComicInfo.Series equals wantSeriesTitle (non-empty; required for Komga
//     series grouping — an empty <Series> causes every chapter to appear as
//     an ungrouped one-off in the Komga library).
func assertKomgaValidCBZ(t *testing.T, cbzPath string, wantSeriesTitle string) {
	t.Helper()

	// G304: path is from a test-controlled temp directory.
	r, err := zip.OpenReader(cbzPath) //nolint:gosec
	if err != nil {
		t.Fatalf("open CBZ: %v", err)
	}
	defer func() { _ = r.Close() }()

	hasComicInfo, pageCount := countCBZEntries(r.File)
	if !hasComicInfo {
		t.Errorf("missing ComicInfo.xml (not Komga-valid)")
	}
	if pageCount == 0 {
		t.Errorf("no image pages found (empty chapter)")
	}

	assertComicInfoSeries(t, cbzPath, wantSeriesTitle)

	t.Logf("CBZ valid: ComicInfo=%v pages=%d", hasComicInfo, pageCount)
}

// countCBZEntries scans a CBZ's entries once and reports whether a ComicInfo.xml
// is present and how many image pages (jpg/jpeg/png/webp/gif/avif/bin) it carries.
func countCBZEntries(files []*zip.File) (hasComicInfo bool, pageCount int) {
	for _, f := range files {
		name := strings.ToLower(f.Name)
		if name == "comicinfo.xml" {
			hasComicInfo = true
			continue
		}
		switch filepath.Ext(name) {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".avif", ".bin":
			pageCount++
		}
	}
	return hasComicInfo, pageCount
}

// assertComicInfoSeries parses the CBZ's ComicInfo.xml via disk.ReadComicInfoFromCBZ
// (a full round-trip) and asserts ComicInfo.Series is non-empty and equals
// wantSeriesTitle — the field Komga uses to group chapters under the correct series.
func assertComicInfoSeries(t *testing.T, cbzPath string, wantSeriesTitle string) {
	t.Helper()

	ci, err := disk.ReadComicInfoFromCBZ(cbzPath)
	if err != nil {
		t.Errorf("parse ComicInfo.xml: %v", err)
	}
	if ci == nil {
		return
	}
	t.Logf("ComicInfo: series=%q chapter=%q pages=%d", ci.Series, ci.Number, ci.PageCount)
	if ci.Series == "" {
		t.Errorf("ComicInfo.Series is empty — Komga cannot group this chapter into a series")
	} else if ci.Series != wantSeriesTitle {
		t.Errorf("ComicInfo.Series: got %q, want %q", ci.Series, wantSeriesTitle)
	}
}

// assertProvenance queries the DB for downloaded chapters and verifies they
// have state=downloaded and a non-zero page_count.
func assertProvenance(t *testing.T, ctx context.Context, db *ent.Client, expectedCount int) {
	t.Helper()

	downloaded, err := db.Chapter.Query().
		Where(entchapter.StateEQ(entchapter.StateDownloaded)).
		All(ctx)
	if err != nil {
		t.Fatalf("query downloaded chapters: %v", err)
	}

	if len(downloaded) != expectedCount {
		t.Errorf("expected %d downloaded chapters, got %d", expectedCount, len(downloaded))
	}

	for _, ch := range downloaded {
		pageCount := 0
		if ch.PageCount != nil {
			pageCount = *ch.PageCount
		}
		if pageCount <= 0 {
			t.Errorf("chapter %s has page_count=%d (expected > 0)", ch.ID, pageCount)
		}
		if ch.Filename == "" {
			t.Errorf("chapter %s has empty filename", ch.ID)
		}
		t.Logf("chapter %s: state=%s pages=%d filename=%s", ch.ID, ch.State, pageCount, ch.Filename)
	}
}

// retryUntil calls fn every 500 ms until it returns nil or timeout elapses.
// The last non-nil error is returned on timeout.
func retryUntil(ctx context.Context, timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout after %s: %w", timeout, lastErr)
}

// TestEngineHardening_PostgresBoot is the MERGE GATE for the embedded
// Suwayomi→Postgres opt-in. It proves end-to-end — against a real embedded
// Suwayomi JVM and an ephemeral testcontainer Postgres — that the database
// -D system properties launch() passes are CORRECT: a wrong key would silently
// fall back to H2 (leaving the Postgres DB empty) or fail to boot.
//
// The proof is two-pronged:
//  1. The server reaches the ready signal and serves a trivial GraphQL read
//     (ServerSettings) — it booted with the supplied DB config.
//  2. The Postgres database actually contains Suwayomi's tables — proving
//     Postgres, not H2, is the live backend (the -D keys took effect).
//
// Discovery note (verified against Suwayomi v2.2.2100 server-reference.conf +
// DBManager.createHikariDataSource): the databaseUrl is the BARE postgresql://
// form; Suwayomi prepends "jdbc:" itself. Keys: server.databaseType /
// databaseUrl / databaseUsername / databasePassword under the
// suwayomi.tachidesk.config. override prefix.
//
// Skips LOUDLY (never silently passes) when Docker is unavailable; Java
// availability is already gated by TestMain (whole binary skips without Java 21+).
func TestEngineHardening_PostgresBoot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	const (
		dbName = "suwayomi"
		dbUser = "suwayomi"
		dbPass = "suwayomi"
	)

	pg, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.BasicWaitStrategies(),
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPass),
	)
	if err != nil {
		t.Skipf("engine-hardening gate: Docker/Postgres unavailable, SKIPPING (not a pass): %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		if termErr := pg.Terminate(stopCtx); termErr != nil {
			t.Logf("engine-hardening gate: terminate postgres container: %v", termErr)
		}
	})

	host, err := pg.Host(ctx)
	if err != nil {
		t.Fatalf("postgres host: %v", err)
	}
	mappedPort, err := pg.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("postgres mapped port: %v", err)
	}

	// Bare postgresql:// form (no jdbc: prefix — Suwayomi adds it). Credentials
	// are passed as separate fields, not embedded in the URL.
	dbURL := fmt.Sprintf("postgresql://%s:%s/%s", host, mappedPort.Port(), dbName)

	javaPath, err := testharness.FindJava21()
	if err != nil {
		t.Skipf("engine-hardening gate: %v", err)
	}

	cfg := config.SuwayomiConfig{
		Host:                "127.0.0.1",
		Port:                "24567", // distinct from the shared harness (14567)
		RuntimeDir:          t.TempDir(),
		Version:             "v2.2.2100",
		DownloadURLTemplate: "https://github.com/Suwayomi/Suwayomi-Server/releases/download/%s/Suwayomi-Server-%s.jar",
		StartTimeout:        5 * time.Minute,
		DownloadTimeout:     15 * time.Minute,
		JavaPath:            javaPath,
		DatabaseType:        "POSTGRESQL",
		DatabaseURL:         dbURL,
		DatabaseUsername:    dbUser,
		DatabasePassword:    dbPass,
	}

	inst := testharness.LaunchOneOff(t, cfg)

	// Prong 1: the server booted and serves GraphQL.
	if _, err := inst.Client().ServerSettings(ctx); err != nil {
		t.Fatalf("ServerSettings after Postgres boot: %v", err)
	}

	// Prong 2: prove Postgres is the live backend (a wrong -D key ⇒ silent H2
	// fallback ⇒ this DB would have zero Suwayomi tables).
	verifyURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPass, host, mappedPort.Port(), dbName)
	db, err := sql.Open("pgx", verifyURL)
	if err != nil {
		t.Fatalf("open postgres for verification: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Count tables across ALL user schemas, not just public: Suwayomi
	// (Exposed + a username-named DB) creates its tables in the "$user"
	// schema — here the schema named after dbUser — so restricting to public
	// would spuriously report zero. Excluding only the two system schemas is
	// robust to whichever schema Suwayomi lands in.
	var tableCount int
	const q = "SELECT count(*) FROM information_schema.tables " +
		"WHERE table_schema NOT IN ('pg_catalog', 'information_schema')"
	if err := db.QueryRowContext(ctx, q).Scan(&tableCount); err != nil {
		t.Fatalf("count Suwayomi tables in Postgres: %v", err)
	}
	if tableCount == 0 {
		t.Fatal("Postgres backend has 0 tables — embedded Suwayomi did NOT use Postgres " +
			"(the -D databaseType/databaseUrl keys are wrong or ineffective)")
	}
	t.Logf("CONFIRMED: embedded Suwayomi booted on Postgres via -D props; %d Suwayomi tables in the Postgres DB", tableCount)
}
