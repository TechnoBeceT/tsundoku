package enginetopo

import (
	"context"
	"encoding/json"
	"errors"
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
//     URL AND version from its repo's index.min.json (fetched via httpGet),
//     download the .apk bytes (httpGet), cache.Put them, read its source ids
//     (client.ExtensionSources), and upsert a HarvestedExtension row whose
//     version_code + apk_sha256 describe the cached bytes (the index entry's own
//     version, not the possibly-older installed version) with apk_cached=true.
//
// It is idempotent: an extension whose row is apk_cached=true AND whose cache
// FILE is present is skipped (no index fetch, no download, no Put) and does NOT
// count toward Cached, so a second run over an unchanged library caches 0 and
// makes zero HTTP calls for those extensions. A row claiming cached but missing
// its file (e.g. the engine volume was recreated) is re-downloaded — the file,
// not the row alone, is the durable truth.
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
// already cached AND its cache file is present — the idempotency skip, which
// does NO network I/O. Any resolution/download/persist failure is returned so
// the caller can record it as a gap.
//
// The two seed-specific behaviours — the idempotency skip and (at the call site)
// gap-recording — live HERE; the actual caching work is delegated to the shared
// recordInstalledExtension core (which the live write-through also drives via the
// exported RecordInstalledExtension), so there is exactly one copy of the
// resolve→download→cache→read-sources→upsert logic.
func seedOneExtension(
	ctx context.Context,
	client suwayomi.Client,
	db *ent.Client,
	cache *apkcache.Store,
	indexes *indexResolver,
	httpGet func(url string) (*http.Response, error),
	ext suwayomi.Extension,
) (cached bool, err error) {
	if already, err := isAlreadyCached(ctx, db, cache, ext); err != nil {
		return false, err
	} else if already {
		return false, nil
	}
	if err := recordInstalledExtension(ctx, client, db, cache, indexes, httpGet, ext); err != nil {
		return false, err
	}
	return true, nil
}

// RecordInstalledExtension caches one installed extension's .apk bytes and
// upserts its HarvestedExtension row — the durable capture of a single installed
// extension. It is the LIVE write-through entry point (see OnExtensionInstalled):
// after the owner installs or updates an extension through Tsundoku, this records
// the just-affected extension into the topology store immediately, without
// waiting for the next boot seed.
//
// It ALWAYS does the work: unlike the seed's seedOneExtension it performs NO
// idempotency skip (an install/update just changed the engine, so the current
// bytes must be re-captured) and records NO gap on failure (the caller decides —
// the write-through logs and continues). Re-capturing an unchanged extension is a
// wasted download but never wrong.
//
// The version_code + apk_sha256 recorded describe the BYTES actually cached: the
// apk is resolved from the repo index (which advertises the latest known-good
// version), so the index entry's OWN version code — not the installed
// ext.VersionCode, which may lag the repo — is cached, named, and recorded;
// installed_version_code stores ext.VersionCode as the seed's change-detector.
// (Installing the latest known-good apk is safe: source ids are stable across
// versions.)
func RecordInstalledExtension(
	ctx context.Context,
	client suwayomi.Client,
	db *ent.Client,
	cache *apkcache.Store,
	httpGet func(url string) (*http.Response, error),
	ext suwayomi.Extension,
) error {
	return recordInstalledExtension(ctx, client, db, cache, newIndexResolver(httpGet), httpGet, ext)
}

// recordInstalledExtension is the resolver-injected caching core shared by the
// boot seed (seedOneExtension, which passes a resolver MEMOISED across the whole
// pass so a repo index is fetched at most once even with several extensions from
// it) and the live write-through (RecordInstalledExtension, which passes a
// one-shot resolver for its single extension). Keeping the resolver a parameter
// is what lets both callers share this one body without the seed losing its
// per-pass index memoisation.
func recordInstalledExtension(
	ctx context.Context,
	client suwayomi.Client,
	db *ent.Client,
	cache *apkcache.Store,
	indexes *indexResolver,
	httpGet func(url string) (*http.Response, error),
	ext suwayomi.Extension,
) error {
	apkURL, indexVersion, err := indexes.resolve(ext.Repo, ext.PkgName)
	if err != nil {
		return err
	}

	sha, err := downloadAndCache(cache, httpGet, apkURL, ext.PkgName, indexVersion, maxAPKBytes)
	if err != nil {
		return err
	}

	sources, err := client.ExtensionSources(ctx, ext.PkgName)
	if err != nil {
		return fmt.Errorf("read extension sources: %w", err)
	}

	row := extensionRow{
		pkgName:              ext.PkgName,
		repoURL:              ext.Repo,
		versionCode:          indexVersion,
		installedVersionCode: ext.VersionCode,
		versionName:          ext.VersionName,
		sourceIDs:            sourceIDs(sources),
		apkSHA256:            sha,
		apkCached:            true,
	}
	if err := upsertExtension(ctx, db, row); err != nil {
		return fmt.Errorf("persist harvested extension: %w", err)
	}
	return nil
}

// maxAPKBytes is the ceiling on how many bytes a single extension .apk download
// may stream into the cache. 256 MiB is orders of magnitude above any real
// manga-source extension apk (they are a few MiB) — a pure safety ceiling against
// a hostile or broken repo streaming unbounded bytes and filling the cache volume,
// NOT a size any legitimate apk approaches.
const maxAPKBytes = 256 << 20

// errAPKTooLarge is returned by a cappedReader once the wrapped stream exceeds
// its byte cap. Surfacing it lets cache.Put fail cleanly (dropping its temp file)
// so the extension is recorded as an uncached gap.
var errAPKTooLarge = errors.New("enginetopo: apk exceeds maximum allowed size")

// cappedReader wraps an io.Reader and returns errAPKTooLarge as soon as more than
// max bytes have been read through it. It exists BECAUSE a bare io.LimitReader is
// wrong here: LimitReader SILENTLY stops at the cap, which would make cache.Put
// commit a TRUNCATED apk (with the wrong sha256) as a SUCCESS — worse than the
// unbounded read it guards against. Erroring instead makes the truncation a clean
// failure the caller records as a gap.
type cappedReader struct {
	r    io.Reader
	max  int64
	read int64
}

// Read reads from the wrapped reader, failing with errAPKTooLarge the instant the
// cumulative bytes read would exceed max (a stream one byte over the cap is
// rejected, never truncated-and-accepted).
func (c *cappedReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	if c.read > c.max {
		return n, errAPKTooLarge
	}
	return n, err
}

// downloadAndCache fetches the .apk at apkURL and streams it into the cache
// under (pkgName, version), returning the sha256 the cache computed. A non-200
// status is an error. The body is streamed through a cappedReader bounded at
// maxBytes, so a hostile/broken repo streaming unbounded bytes fails cleanly (no
// partial apk is cached — cache.Put drops its temp file on the read error) rather
// than filling the cache volume.
func downloadAndCache(
	cache *apkcache.Store,
	httpGet func(url string) (*http.Response, error),
	apkURL, pkgName string,
	version int,
	maxBytes int64,
) (string, error) {
	resp, err := httpGet(apkURL)
	if err != nil {
		return "", fmt.Errorf("download apk %q: %w", apkURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download apk %q: status %d", apkURL, resp.StatusCode)
	}
	sha, _, err := cache.Put(pkgName, version, &cappedReader{r: resp.Body, max: maxBytes})
	if err != nil {
		return "", fmt.Errorf("cache apk: %w", err)
	}
	return sha, nil
}

// isAlreadyCached reports whether ext is already stored with apk_cached=true, its
// cache FILE is present, AND the INSTALLED version is unchanged since those bytes
// were cached — the idempotency guard that makes a re-run a no-op.
//
// The file check is load-bearing: the DB row lives in Postgres but the bytes live
// on the engine volume, so a row alone must never be trusted (a recreated volume
// would leave a "cached" row 404ing at recovery time). When the file is absent the
// extension is re-downloaded even though the row claims cached.
//
// The version check keys off the INSTALLED version, not the index version. The two
// axes are distinct: version_code is the repo-INDEX version of the cached bytes,
// while installed_version_code is what the engine had INSTALLED when they were
// cached. We skip only when the installed version is unchanged
// (existing.InstalledVersionCode == ext.VersionCode); on ANY installed-version
// change — up (owner upgraded) OR down (owner sideloaded a build the repo later
// rolled back below) — we re-resolve + re-download, which restores
// installed_version_code == ext.VersionCode, so the very next boot skips again.
//
// Equality, NOT the old `ext.VersionCode <= existing.VersionCode`: that compared
// two different axes (installed vs index), so an installed version that
// PERSISTENTLY EXCEEDED the index version was `<=`-false on every boot and
// re-downloaded from the upstream repo forever — an unbounded-in-time refetch loop
// and an anti-ban hazard. Keying on installed-version equality re-caches exactly
// once per installed-version change and never loops, regardless of whether the
// repo index leads or lags the installed version.
func isAlreadyCached(ctx context.Context, db *ent.Client, cache *apkcache.Store, ext suwayomi.Extension) (bool, error) {
	existing, err := db.HarvestedExtension.Query().
		Where(entharvestedextension.PkgName(ext.PkgName)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query harvested extension: %w", err)
	}
	return existing.ApkCached &&
		cache.Exists(ext.PkgName, existing.VersionCode) &&
		existing.InstalledVersionCode == ext.VersionCode, nil
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
	pkgName string
	repoURL string
	// versionCode is the repo-INDEX version describing the cached apk bytes.
	versionCode int
	// installedVersionCode is the engine-INSTALLED version at cache time — the
	// change-detector isAlreadyCached compares against to decide a re-cache.
	installedVersionCode int
	versionName          string
	sourceIDs            []int64
	apkSHA256            string
	apkCached            bool
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
			SetInstalledVersionCode(row.installedVersionCode).
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
		SetInstalledVersionCode(row.installedVersionCode).
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
		pkgName:              ext.PkgName,
		repoURL:              ext.Repo,
		versionCode:          ext.VersionCode,
		installedVersionCode: ext.VersionCode,
		versionName:          ext.VersionName,
		apkCached:            false,
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

// maxIndexBytes bounds how much of a repo's index.min.json is read into memory.
// 16 MiB is far above any real index (the largest community repos are a few MiB)
// yet cheap insurance against a hostile or corrupt endpoint streaming forever.
const maxIndexBytes = 16 << 20

// repoIndexEntry is one extension entry from a repo's index.min.json (only the
// fields we need; unknown fields are ignored). It mirrors engine-host's
// RepoIndexEntry.
type repoIndexEntry struct {
	// Pkg is the extension's Android package name (matches Extension.PkgName).
	Pkg string `json:"pkg"`
	// Apk is the .apk file name, resolved against "<repoBase>/apk/<apk>".
	Apk string `json:"apk"`
	// Code is the entry's own numeric version code — the version of the BYTES
	// this entry points at, recorded so the stored version_code describes the
	// cached apk rather than the (possibly older) installed version.
	Code int `json:"code"`
}

// indexResult memoises one repo's index fetch (entries or the failure), so a
// broken repo is fetched at most once per pass even with several extensions.
type indexResult struct {
	entries []repoIndexEntry
	err     error
}

// indexResolver fetches + caches repo index.min.json documents and resolves an
// extension's .apk download URL + version from them, mirroring engine-host's URL
// scheme.
type indexResolver struct {
	httpGet func(url string) (*http.Response, error)
	byRepo  map[string]indexResult
}

// newIndexResolver builds an indexResolver over httpGet.
func newIndexResolver(httpGet func(url string) (*http.Response, error)) *indexResolver {
	return &indexResolver{httpGet: httpGet, byRepo: make(map[string]indexResult)}
}

// resolve returns the .apk download URL AND the version code for pkgName within
// repoURL's index. The version is the index entry's own Code (the version of the
// bytes the URL points at), so the caller records metadata that matches the
// cached file. It errors when the repo url is blank, the index cannot be
// fetched/parsed, or the index has no entry for pkgName.
func (r *indexResolver) resolve(repoURL, pkgName string) (apkURL string, version int, err error) {
	if strings.TrimSpace(repoURL) == "" {
		return "", 0, fmt.Errorf("extension %q has no repo url", pkgName)
	}
	entries, err := r.entriesFor(repoURL)
	if err != nil {
		return "", 0, err
	}
	for _, e := range entries {
		if e.Pkg == pkgName {
			return apkURLFor(repoURL, e.Apk), e.Code, nil
		}
	}
	return "", 0, fmt.Errorf("extension %q not found in repo index %q", pkgName, repoURL)
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
	// Cap the read so a hostile/oversized index can't OOM the process. The apk
	// download itself streams straight into the cache and needs no such cap.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes))
	if err != nil {
		return nil, fmt.Errorf("read repo index %q: %w", indexURL, err)
	}
	var entries []repoIndexEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("parse repo index %q: %w", indexURL, err)
	}
	return entries, nil
}

// repoBaseURL normalises a stored repo URL to its base DIRECTORY — the parent the
// index + apk paths hang off — regardless of whether Suwayomi stored the bare repo
// directory or a URL pointing straight at the repo's INDEX FILE.
//
// Suwayomi stores whatever repo URL the owner (or a default) configured, and the
// real-world value points at the index file, e.g.
//
//	https://raw.githubusercontent.com/keiyoushi/extensions/repo/index.pb
//
// The base directory of that is ".../repo" — NOT the whole URL. The rule: trim a
// trailing "/", then if the LAST path segment looks like a Mihon index file (ends
// in ".json" or ".pb", case-insensitive — covers index.min.json, index.json, and
// the newer protobuf index.pb) strip that segment and return the parent directory;
// otherwise the URL is already a base directory and is returned unchanged. A last
// segment with no such extension (a bare ".../repo") is never stripped, so a
// directory URL is preserved.
//
// This is the fix for the prod 404: the old code special-cased only a ".json"
// suffix, so a ".pb" index URL was treated as a directory and got "/index.min.json"
// appended onto the FILE name, producing ".../repo/index.pb/index.min.json" → 404
// for every extension. Deriving the base first makes both the index and apk URLs
// correct for either stored shape.
//
// NOTE: engine-host's ExtensionManager (the Kotlin Phase-2 side this Go path
// mirrors) carries the SAME index-file-vs-directory bug and needs the same fix —
// its indexUrlFor/repoBaseFor only special-case ".json" too.
func repoBaseURL(repoURL string) string {
	trimmed := strings.TrimRight(repoURL, "/")
	slash := strings.LastIndex(trimmed, "/")
	if slash < 0 {
		return trimmed
	}
	last := strings.ToLower(trimmed[slash+1:])
	if strings.HasSuffix(last, ".json") || strings.HasSuffix(last, ".pb") {
		return trimmed[:slash]
	}
	return trimmed
}

// indexURLFor builds a repo's index.min.json URL from its base directory, so an
// index-FILE repo URL (index.pb / index.min.json / index.json) and a bare repo
// directory both resolve to "<base>/index.min.json". Mirrors engine-host
// ExtensionManager.indexUrlFor (see repoBaseURL's NOTE — the engine-host side needs
// the same fix).
func indexURLFor(repoURL string) string {
	return repoBaseURL(repoURL) + "/index.min.json"
}

// repoBaseFor resolves the base URL an APK is relative to — the repo's base
// directory (see repoBaseURL). Mirrors engine-host ExtensionManager.repoBaseFor.
func repoBaseFor(repoURL string) string {
	return repoBaseURL(repoURL)
}

// apkURLFor builds an extension's .apk download URL: "<repoBase>/apk/<apk>",
// yielding ".../repo/apk/<file>" for both a bare-directory and an index-file repo
// URL. Mirrors engine-host ExtensionManager.apkUrlFor.
func apkURLFor(repoURL, apk string) string {
	return repoBaseFor(repoURL) + "/apk/" + apk
}
