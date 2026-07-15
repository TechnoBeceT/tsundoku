package enginetopo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	entharvestedrepo "github.com/technobecet/tsundoku/internal/ent/harvestedrepo"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// Result reports what a SeedExtensions pass did.
type Result struct {
	// Repos is the number of HarvestedRepo rows upserted from the engine's
	// configured repo URL list.
	Repos int
	// Cached is the number of installed extensions whose .apk was freshly
	// downloaded and cached on THIS pass (a re-run over an already-cached,
	// same-version library reports 0).
	Cached int
	// Gaps is the number of installed extensions that could NOT be cached (a
	// dead repo, a missing index entry, a download failure, …). Each such
	// extension is still recorded with apk_cached=false so the gap is visible.
	Gaps int
}

// SeedExtensions reads the live engine's repos + installed extensions into
// Tsundoku's own durable engine-topology store (HarvestedRepo / HarvestedExtension)
// and caches each installed extension's .apk bytes, so the extension set can be
// recovered later even if the upstream repo is offline.
//
// Flow:
//  1. client.ExtensionRepos → upsert one HarvestedRepo row per URL.
//  2. client.Extensions → for each INSTALLED extension: resolve its .apk download
//     URL from its repo's index.min.json (fetched via httpGet), download the .apk
//     bytes (httpGet), cache.Put them, read its source ids
//     (client.ExtensionSources), and upsert a HarvestedExtension row with
//     apk_sha256 + apk_cached=true.
//
// It is idempotent: an extension already cached at its current version is
// skipped (no index fetch, no download, no Put) and does NOT count toward
// Cached, so a second run over an unchanged library caches 0 and makes zero
// HTTP calls for those extensions.
//
// Partial success: a per-extension failure is logged (slog.Warn), recorded with
// apk_cached=false, and counted in Gaps — one dead repo never aborts the pass.
// err is non-nil only when an ENUMERATING call fails (listing repos, listing
// extensions, or persisting a repo row), because those leave the whole pass
// unable to proceed.
func SeedExtensions(
	ctx context.Context,
	client suwayomi.Client,
	db *ent.Client,
	cache *apkcache.Store,
	httpGet func(url string) (*http.Response, error),
) (Result, error) {
	var res Result

	repoURLs, err := client.ExtensionRepos(ctx)
	if err != nil {
		return res, fmt.Errorf("enginetopo.SeedExtensions: list repos: %w", err)
	}
	for _, url := range repoURLs {
		if err := upsertRepo(ctx, db, url); err != nil {
			return res, fmt.Errorf("enginetopo.SeedExtensions: upsert repo %q: %w", url, err)
		}
		res.Repos++
	}

	exts, err := client.Extensions(ctx)
	if err != nil {
		return res, fmt.Errorf("enginetopo.SeedExtensions: list extensions: %w", err)
	}

	indexes := newIndexResolver(httpGet)
	for _, ext := range exts {
		if !ext.IsInstalled {
			continue
		}
		cached, err := seedOneExtension(ctx, client, db, cache, indexes, httpGet, ext)
		if err != nil {
			slog.WarnContext(ctx, "enginetopo: could not cache extension apk, recording gap",
				"pkg_name", ext.PkgName, "repo", ext.Repo, "version_code", ext.VersionCode, "err", err)
			recordGap(ctx, db, ext)
			res.Gaps++
			continue
		}
		if cached {
			res.Cached++
		}
	}

	return res, nil
}

// seedOneExtension caches one installed extension's .apk and upserts its
// HarvestedExtension row. It returns cached=true when it freshly downloaded and
// cached the apk, and cached=false (with a nil error) when the extension was
// already cached at this version — the idempotency skip, which does NO network
// I/O. Any resolution/download/persist failure is returned so the caller can
// record it as a gap.
func seedOneExtension(
	ctx context.Context,
	client suwayomi.Client,
	db *ent.Client,
	cache *apkcache.Store,
	indexes *indexResolver,
	httpGet func(url string) (*http.Response, error),
	ext suwayomi.Extension,
) (cached bool, err error) {
	if already, err := isAlreadyCached(ctx, db, ext); err != nil {
		return false, err
	} else if already {
		return false, nil
	}

	apkURL, err := indexes.apkURL(ext.Repo, ext.PkgName)
	if err != nil {
		return false, err
	}

	sha, err := downloadAndCache(ctx, cache, httpGet, apkURL, ext)
	if err != nil {
		return false, err
	}

	sources, err := client.ExtensionSources(ctx, ext.PkgName)
	if err != nil {
		return false, fmt.Errorf("read extension sources: %w", err)
	}

	row := extensionRow{
		pkgName:     ext.PkgName,
		repoURL:     ext.Repo,
		versionCode: ext.VersionCode,
		versionName: ext.VersionName,
		sourceIDs:   sourceIDs(sources),
		apkSHA256:   sha,
		apkCached:   true,
	}
	if err := upsertExtension(ctx, db, row); err != nil {
		return false, fmt.Errorf("persist harvested extension: %w", err)
	}
	return true, nil
}

// downloadAndCache fetches the .apk at apkURL and streams it into the cache,
// returning the sha256 the cache computed. A non-200 status is an error.
func downloadAndCache(
	ctx context.Context,
	cache *apkcache.Store,
	httpGet func(url string) (*http.Response, error),
	apkURL string,
	ext suwayomi.Extension,
) (string, error) {
	resp, err := httpGet(apkURL)
	if err != nil {
		return "", fmt.Errorf("download apk %q: %w", apkURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download apk %q: status %d", apkURL, resp.StatusCode)
	}
	sha, _, err := cache.Put(ext.PkgName, ext.VersionCode, resp.Body)
	if err != nil {
		return "", fmt.Errorf("cache apk: %w", err)
	}
	return sha, nil
}

// isAlreadyCached reports whether ext is already stored at its CURRENT version
// with its apk cached — the idempotency guard that makes a re-run a no-op.
func isAlreadyCached(ctx context.Context, db *ent.Client, ext suwayomi.Extension) (bool, error) {
	existing, err := db.HarvestedExtension.Query().
		Where(entharvestedextension.PkgName(ext.PkgName)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query harvested extension: %w", err)
	}
	return existing.ApkCached && existing.VersionCode == ext.VersionCode, nil
}

// sourceIDs converts the Suwayomi sources an extension provides into the int64
// ids stored on the row. Source.ID is a 64-bit integer serialised as a string;
// an unparseable id is skipped (never fails the whole extension).
func sourceIDs(sources []suwayomi.Source) []int64 {
	ids := make([]int64, 0, len(sources))
	for _, s := range sources {
		id, err := strconv.ParseInt(s.ID, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// extensionRow is the flat set of fields written to a HarvestedExtension row,
// keeping upsertExtension's signature small and self-documenting.
type extensionRow struct {
	pkgName     string
	repoURL     string
	versionCode int
	versionName string
	sourceIDs   []int64
	apkSHA256   string
	apkCached   bool
}

// upsertExtension find-or-creates a HarvestedExtension by pkg_name (its stable
// identity) and writes row's fields — the query-then-write pattern the rest of
// the ingest engine uses (there is no Ent upsert helper generated for this
// entity). SeedExtensions iterates extensions serially, so there is no
// concurrent-writer race to guard.
func upsertExtension(ctx context.Context, db *ent.Client, row extensionRow) error {
	existing, err := db.HarvestedExtension.Query().
		Where(entharvestedextension.PkgName(row.pkgName)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return db.HarvestedExtension.Create().
			SetPkgName(row.pkgName).
			SetRepoURL(row.repoURL).
			SetVersionCode(row.versionCode).
			SetVersionName(row.versionName).
			SetSourceIds(row.sourceIDs).
			SetApkSha256(row.apkSHA256).
			SetApkCached(row.apkCached).
			Exec(ctx)
	}
	if err != nil {
		return err
	}
	return db.HarvestedExtension.UpdateOne(existing).
		SetRepoURL(row.repoURL).
		SetVersionCode(row.versionCode).
		SetVersionName(row.versionName).
		SetSourceIds(row.sourceIDs).
		SetApkSha256(row.apkSHA256).
		SetApkCached(row.apkCached).
		Exec(ctx)
}

// recordGap upserts a HarvestedExtension for an extension that could NOT be
// cached, marking apk_cached=false so the gap is visible in the store. It is
// best-effort: a failure to record the gap is logged and swallowed (the pass
// must not abort because it could not persist a gap marker).
func recordGap(ctx context.Context, db *ent.Client, ext suwayomi.Extension) {
	row := extensionRow{
		pkgName:     ext.PkgName,
		repoURL:     ext.Repo,
		versionCode: ext.VersionCode,
		versionName: ext.VersionName,
		apkCached:   false,
	}
	if err := upsertExtension(ctx, db, row); err != nil {
		slog.WarnContext(ctx, "enginetopo: failed to record extension gap",
			"pkg_name", ext.PkgName, "err", err)
	}
}

// upsertRepo find-or-creates a HarvestedRepo by url (its stable identity). A
// re-seed of an existing repo is a no-op create-skip (the row already carries
// the url and its updated_at is refreshed on any real write elsewhere).
func upsertRepo(ctx context.Context, db *ent.Client, url string) error {
	exists, err := db.HarvestedRepo.Query().
		Where(entharvestedrepo.URL(url)).
		Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return db.HarvestedRepo.Create().SetURL(url).Exec(ctx)
}

// --- Mihon repo index resolution --------------------------------------------

// repoIndexEntry is one extension entry from a repo's index.min.json (only the
// fields we need; unknown fields are ignored). It mirrors engine-host's
// RepoIndexEntry.
type repoIndexEntry struct {
	// Pkg is the extension's Android package name (matches Extension.PkgName).
	Pkg string `json:"pkg"`
	// Apk is the .apk file name, resolved against "<repoBase>/apk/<apk>".
	Apk string `json:"apk"`
}

// indexResult memoises one repo's index fetch (entries or the failure), so a
// broken repo is fetched at most once per pass even with several extensions.
type indexResult struct {
	entries []repoIndexEntry
	err     error
}

// indexResolver fetches + caches repo index.min.json documents and resolves an
// extension's .apk download URL from them, mirroring engine-host's URL scheme.
type indexResolver struct {
	httpGet func(url string) (*http.Response, error)
	byRepo  map[string]indexResult
}

// newIndexResolver builds an indexResolver over httpGet.
func newIndexResolver(httpGet func(url string) (*http.Response, error)) *indexResolver {
	return &indexResolver{httpGet: httpGet, byRepo: make(map[string]indexResult)}
}

// apkURL resolves the .apk download URL for pkgName within repoURL's index. It
// errors when the repo url is blank, the index cannot be fetched/parsed, or the
// index has no entry for pkgName.
func (r *indexResolver) apkURL(repoURL, pkgName string) (string, error) {
	if strings.TrimSpace(repoURL) == "" {
		return "", fmt.Errorf("extension %q has no repo url", pkgName)
	}
	entries, err := r.entriesFor(repoURL)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Pkg == pkgName {
			return apkURLFor(repoURL, e.Apk), nil
		}
	}
	return "", fmt.Errorf("extension %q not found in repo index %q", pkgName, repoURL)
}

// entriesFor fetches and parses repoURL's index.min.json, memoising the result
// (success or failure) for the pass.
func (r *indexResolver) entriesFor(repoURL string) ([]repoIndexEntry, error) {
	if cached, ok := r.byRepo[repoURL]; ok {
		return cached.entries, cached.err
	}
	entries, err := fetchIndex(r.httpGet, repoURL)
	r.byRepo[repoURL] = indexResult{entries: entries, err: err}
	return entries, err
}

// fetchIndex GETs and decodes a repo's index.min.json.
func fetchIndex(httpGet func(url string) (*http.Response, error), repoURL string) ([]repoIndexEntry, error) {
	indexURL := indexURLFor(repoURL)
	resp, err := httpGet(indexURL)
	if err != nil {
		return nil, fmt.Errorf("fetch repo index %q: %w", indexURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch repo index %q: status %d", indexURL, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read repo index %q: %w", indexURL, err)
	}
	var entries []repoIndexEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("parse repo index %q: %w", indexURL, err)
	}
	return entries, nil
}

// indexURLFor builds a repo's index.min.json URL. A repo URL that already points
// at a .json document is used verbatim; otherwise "index.min.json" is appended.
// Mirrors engine-host ExtensionManager.indexUrlFor.
func indexURLFor(repoURL string) string {
	if strings.HasSuffix(repoURL, ".json") {
		return repoURL
	}
	return strings.TrimRight(repoURL, "/") + "/index.min.json"
}

// repoBaseFor resolves the base URL an APK is relative to: the directory holding
// a .json index, or the trimmed repo root otherwise. Mirrors engine-host
// ExtensionManager.repoBaseFor.
func repoBaseFor(repoURL string) string {
	if strings.HasSuffix(repoURL, ".json") {
		if i := strings.LastIndex(repoURL, "/"); i >= 0 {
			return repoURL[:i]
		}
		return repoURL
	}
	return strings.TrimRight(repoURL, "/")
}

// apkURLFor builds an extension's .apk download URL: "<repoBase>/apk/<apk>".
// Mirrors engine-host ExtensionManager.apkUrlFor.
func apkURLFor(repoURL, apk string) string {
	return repoBaseFor(repoURL) + "/apk/" + apk
}
