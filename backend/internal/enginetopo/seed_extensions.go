package enginetopo

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	entharvestedrepo "github.com/technobecet/tsundoku/internal/ent/harvestedrepo"
	"github.com/technobecet/tsundoku/internal/sourceengine"
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

// SeedExtensionsExact captures installed generations from the engine itself.
// Repository URLs are retained as topology metadata only; they are never used
// as a substitute for the currently installed bytes.
func SeedExtensionsExact(ctx context.Context, client sourceengine.Client, db *ent.Client, cache *apkcache.Store, archive *ExtensionArchive) (Result, error) {
	var res Result
	repoURLs, err := client.Repos(ctx)
	if err != nil {
		return res, fmt.Errorf("enginetopo.SeedExtensionsExact: list repos: %w", err)
	}
	for _, repoURL := range repoURLs {
		if err := upsertRepo(ctx, db, repoURL); err != nil {
			return res, fmt.Errorf("enginetopo.SeedExtensionsExact: upsert repo %q: %w", repoURL, err)
		}
		res.Repos++
	}
	res.Cached, res.Gaps, err = archive.SeedInstalled(ctx)
	if err != nil {
		return res, fmt.Errorf("enginetopo.SeedExtensionsExact: seed installed extensions: %w", err)
	}
	return res, nil
}

func exactExtensionCached(ctx context.Context, db *ent.Client, cache *apkcache.Store, ext sourceengine.Extension) bool {
	row, err := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName(ext.PkgName)).Only(ctx)
	return err == nil && row.ApkCached && row.VersionCode == int(ext.VersionCode) &&
		row.InstalledVersionCode == int(ext.VersionCode) && cache.Exists(ext.PkgName, int(ext.VersionCode))
}

// SeedExtensions reads the live engine's repos + installed extensions into
// Tsundoku's own durable engine-topology store (HarvestedRepo / HarvestedExtension)
// and caches each installed extension's .apk bytes, so the extension set can be
// recovered later even if the upstream repo is offline.
//
// Flow:
//  1. client.Repos → upsert one HarvestedRepo row per URL.
//  2. client.Extensions → for each INSTALLED extension: resolve its .apk download
//     URL AND version from its configured repository index (wrapper JSON, legacy
//     array JSON, or protobuf, fetched via httpGet), download the .apk bytes,
//     cache.Put them, and upsert a
//     HarvestedExtension row whose version_code + apk_sha256 describe the cached
//     bytes (the index entry's own version, not the possibly-older installed
//     version) with apk_cached=true. The extension's source ids come straight
//     off ext.Sources (embedded on the wire — no separate lookup call).
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
	client sourceengine.Client,
	db *ent.Client,
	cache *apkcache.Store,
	httpGet func(context.Context, string) (*http.Response, error),
	retainedFn func(context.Context) int,
) (Result, error) {
	var res Result
	// The rollback-history depth is read ONCE per pass (a seed is one-shot); the
	// prune keeps this many versions ∪ the installed version per extension.
	retained := resolveRetained(ctx, retainedFn)

	repoURLs, err := client.Repos(ctx)
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

	indexes := newIndexResolver(ctx, httpGet)
	for _, ext := range exts {
		if !ext.IsInstalled {
			continue
		}
		cached, err := seedOneExtension(ctx, db, cache, indexes, httpGet, ext, retained)
		if err != nil {
			slog.WarnContext(ctx, "enginetopo: could not cache extension apk, recording gap",
				"pkg_name", ext.PkgName, "repo", repoURLOf(ext), "version_code", ext.VersionCode, "err", err)
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

// repoURLOf resolves an extension's repo URL, guarding sourceengine's nullable
// RepoURL (nil when the extension is not associated with a configured repo,
// e.g. sideloaded) down to "" — the "no repo" sentinel every caller below
// (isAlreadyCached, recordGap, indexResolver.resolve) already expects.
func repoURLOf(ext sourceengine.Extension) string {
	if ext.RepoURL == nil {
		return ""
	}
	return *ext.RepoURL
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
// resolve→download→cache→upsert logic.
func seedOneExtension(
	ctx context.Context,
	db *ent.Client,
	cache *apkcache.Store,
	indexes *indexResolver,
	httpGet func(context.Context, string) (*http.Response, error),
	ext sourceengine.Extension,
	retained int,
) (cached bool, err error) {
	if already, err := isAlreadyCached(ctx, db, cache, ext); err != nil {
		return false, err
	} else if already {
		return false, nil
	}
	if err := recordInstalledExtension(ctx, db, cache, indexes, httpGet, ext, retained, false); err != nil {
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
// The live write-through is stricter than the boot seed: the repository entry's
// version MUST equal ext.VersionCode, the post-mutation version the engine says
// it actually installed. A repository refresh racing ahead to a newer candidate
// therefore fails capture without downloading or disturbing the prior held
// generation; candidate bytes are never misrepresented as recovery bytes for
// the running version.
func RecordInstalledExtension(
	ctx context.Context,
	db *ent.Client,
	cache *apkcache.Store,
	httpGet func(context.Context, string) (*http.Response, error),
	ext sourceengine.Extension,
	retained int,
) error {
	return recordInstalledExtension(ctx, db, cache, newIndexResolver(ctx, httpGet), httpGet, ext, retained, true)
}

// recordInstalledExtension is the resolver-injected caching core shared by the
// boot seed (seedOneExtension, which passes a resolver MEMOISED across the whole
// pass so a repo index is fetched at most once even with several extensions from
// it) and the live write-through (RecordInstalledExtension, which passes a
// one-shot resolver for its single extension). Keeping the resolver a parameter
// is what lets both callers share this one body without the seed losing its
// per-pass index memoisation.
//
// requireInstalledVersion is true only for live write-through, where the
// post-mutation installed version is an exact witness. The boot seed keeps its
// established latest-index capture behavior for an older installed build.
func recordInstalledExtension(
	ctx context.Context,
	db *ent.Client,
	cache *apkcache.Store,
	indexes *indexResolver,
	httpGet func(context.Context, string) (*http.Response, error),
	ext sourceengine.Extension,
	retained int,
	requireInstalledVersion bool,
) error {
	repoURL := repoURLOf(ext)
	apkURL, indexVersion, err := indexes.resolve(repoURL, ext.PkgName)
	if err != nil {
		return err
	}
	if requireInstalledVersion && indexVersion != int(ext.VersionCode) {
		return fmt.Errorf(
			"extension %q repository version %d does not match installed version %d",
			ext.PkgName, indexVersion, ext.VersionCode,
		)
	}

	sha, err := downloadAndCache(ctx, cache, httpGet, apkURL, ext.PkgName, indexVersion, maxAPKBytes)
	if err != nil {
		return err
	}

	// Retention: with the freshly-cached apk now on disk, prune this package's
	// cached versions to the newest `retained` ∪ the installed version, and record
	// the resulting held set on the row (the reversible-update history the UI lists).
	cachedVersions := pruneAndBuildCachedVersions(
		ctx, db, cache, ext.PkgName, retained, indexVersion, ext.VersionName, int(ext.VersionCode),
	)

	row := extensionRow{
		pkgName:              ext.PkgName,
		repoURL:              repoURL,
		versionCode:          indexVersion,
		installedVersionCode: int(ext.VersionCode),
		versionName:          ext.VersionName,
		sourceIDs:            sourceIDs(ext.Sources),
		apkSHA256:            sha,
		apkCached:            true,
		cachedVersions:       cachedVersions,
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
	ctx context.Context,
	cache *apkcache.Store,
	httpGet func(context.Context, string) (*http.Response, error),
	apkURL, pkgName string,
	version int,
	maxBytes int64,
) (string, error) {
	resp, err := httpGet(ctx, apkURL)
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
func isAlreadyCached(ctx context.Context, db *ent.Client, cache *apkcache.Store, ext sourceengine.Extension) (bool, error) {
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
		existing.InstalledVersionCode == int(ext.VersionCode), nil
}

// sourceIDs copies the stable source ids an extension provides. sourceengine
// already reports them as int64 on Extension.Sources (one Source struct per
// language the extension supports) — a plain field copy, unlike the retired
// Suwayomi GraphQL shape, which serialised a 64-bit id as a string that had to
// be parsed (and a parse failure silently skipped).
func sourceIDs(sources []sourceengine.Source) []int64 {
	ids := make([]int64, len(sources))
	for i, s := range sources {
		ids[i] = s.ID
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
	// cachedVersions is the held-version set to store (nil ⇒ keep the row's
	// existing set unchanged on update — see recordGap, which preserves it).
	cachedVersions []apkcache.CachedVersion
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
			SetCachedVersions(row.cachedVersions).
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
		SetCachedVersions(row.cachedVersions).
		Exec(ctx)
}

// recordGap upserts a HarvestedExtension for an extension that could NOT be
// cached, marking apk_cached=false so the gap is visible in the store. It is
// best-effort: a failure to record the gap is logged and swallowed (the pass
// must not abort because it could not persist a gap marker).
func recordGap(ctx context.Context, db *ent.Client, ext sourceengine.Extension) {
	row := extensionRow{
		pkgName:              ext.PkgName,
		repoURL:              repoURLOf(ext),
		versionCode:          int(ext.VersionCode),
		installedVersionCode: int(ext.VersionCode),
		versionName:          ext.VersionName,
		apkCached:            false,
		// Preserve any already-held versions: a transient caching failure must not
		// wipe the extension's rollback history.
		cachedVersions: loadCachedVersions(ctx, db, ext.PkgName),
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

// maxIndexBytes bounds how much of a repository index is read into memory. 16
// MiB is far above any real JSON/protobuf index yet cheap insurance against a
// hostile or corrupt endpoint streaming forever.
const maxIndexBytes = 16 << 20

// repoIndexEntry is the format-neutral subset of one repository entry that the
// durable capture needs. APKURL is normalized to absolute while parsing: wrapper
// JSON/protobuf carry it directly; legacy arrays resolve their relative `apk`
// filename against the repository base.
type repoIndexEntry struct {
	Pkg    string
	APKURL string
	Code   int
}

type legacyRepoIndexEntry struct {
	Pkg  string `json:"pkg"`
	Apk  string `json:"apk"`
	Code int    `json:"code"`
}

type wrappedRepoIndex struct {
	ExtensionList struct {
		Extensions []wrappedRepoIndexEntry `json:"extensions"`
	} `json:"extensionList"`
}

type wrappedRepoIndexEntry struct {
	PackageName string            `json:"packageName"`
	VersionCode repoVersionCode   `json:"versionCode"`
	Resources   repoIndexResource `json:"resources"`
}

type repoIndexResource struct {
	APKURL string `json:"apkUrl"`
}

// repoVersionCode accepts both the current wrapper's quoted decimal and a JSON
// number, so a repository changing only its scalar encoding cannot erase the
// catalogue from the capture path.
type repoVersionCode int

func (c *repoVersionCode) UnmarshalJSON(data []byte) error {
	var raw string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
	} else {
		raw = string(data)
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("invalid repository version code %q: %w", raw, err)
	}
	*c = repoVersionCode(value)
	return nil
}

// indexResult memoises one repo's index fetch (entries or the failure), so a
// broken repo is fetched at most once per pass even with several extensions.
type indexResult struct {
	entries []repoIndexEntry
	err     error
}

// indexResolver fetches + caches configured repository index documents and
// resolves an extension's absolute .apk download URL + version from them.
type indexResolver struct {
	ctx     context.Context
	httpGet func(context.Context, string) (*http.Response, error)
	byRepo  map[string]indexResult
}

// newIndexResolver builds an indexResolver over httpGet.
func newIndexResolver(ctx context.Context, httpGet func(context.Context, string) (*http.Response, error)) *indexResolver {
	return &indexResolver{ctx: ctx, httpGet: httpGet, byRepo: make(map[string]indexResult)}
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
			if strings.TrimSpace(e.APKURL) == "" {
				return "", 0, fmt.Errorf("extension %q has no apk url in repo index %q", pkgName, repoURL)
			}
			return e.APKURL, e.Code, nil
		}
	}
	return "", 0, fmt.Errorf("extension %q not found in repo index %q", pkgName, repoURL)
}

// entriesFor fetches and parses repoURL's configured index, memoising the result
// (success or failure) for the pass.
func (r *indexResolver) entriesFor(repoURL string) ([]repoIndexEntry, error) {
	if cached, ok := r.byRepo[repoURL]; ok {
		return cached.entries, cached.err
	}
	entries, err := fetchIndex(r.ctx, r.httpGet, repoURL)
	r.byRepo[repoURL] = indexResult{entries: entries, err: err}
	return entries, err
}

// fetchIndex GETs the repository's configured index URL and decodes every format
// the engine host accepts: current wrapper JSON, legacy top-level JSON arrays,
// and (optionally gzip-compressed) protobuf.
func fetchIndex(ctx context.Context, httpGet func(context.Context, string) (*http.Response, error), repoURL string) ([]repoIndexEntry, error) {
	indexURL := indexURLFor(repoURL)
	resp, err := httpGet(ctx, indexURL)
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
	entries, err := parseRepoIndex(body, indexURL, repoURL)
	if err != nil {
		return nil, fmt.Errorf("parse repo index %q: %w", indexURL, err)
	}
	return entries, nil
}

func parseRepoIndex(body []byte, indexURL, repoURL string) ([]repoIndexEntry, error) {
	if isProtobufIndex(indexURL, body) {
		return parseProtoRepoIndex(body)
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, errors.New("empty repository index")
	}
	switch trimmed[0] {
	case '[':
		var legacy []legacyRepoIndexEntry
		if err := json.Unmarshal(trimmed, &legacy); err != nil {
			return nil, err
		}
		entries := make([]repoIndexEntry, 0, len(legacy))
		for _, entry := range legacy {
			entries = append(entries, repoIndexEntry{
				Pkg: entry.Pkg, APKURL: apkURLFor(repoURL, entry.Apk), Code: entry.Code,
			})
		}
		return entries, nil
	case '{':
		var wrapped wrappedRepoIndex
		if err := json.Unmarshal(trimmed, &wrapped); err != nil {
			return nil, err
		}
		entries := make([]repoIndexEntry, 0, len(wrapped.ExtensionList.Extensions))
		for _, entry := range wrapped.ExtensionList.Extensions {
			entries = append(entries, repoIndexEntry{
				Pkg: entry.PackageName, APKURL: entry.Resources.APKURL, Code: int(entry.VersionCode),
			})
		}
		return entries, nil
	default:
		return nil, fmt.Errorf("unsupported repository index prefix 0x%02x", trimmed[0])
	}
}

func isProtobufIndex(indexURL string, body []byte) bool {
	return strings.HasSuffix(strings.ToLower(indexURL), ".pb") ||
		(len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b)
}

func parseProtoRepoIndex(body []byte) ([]repoIndexEntry, error) {
	raw, err := decompressProtoIndex(body)
	if err != nil {
		return nil, err
	}
	var entries []repoIndexEntry
	err = walkProtoFields(raw, func(number int, wireType uint64, value []byte, _ uint64) error {
		return collectProtoExtensionList(number, wireType, value, &entries)
	})
	return entries, err
}

func decompressProtoIndex(body []byte) ([]byte, error) {
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		return body, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	decompressed, readErr := io.ReadAll(io.LimitReader(reader, maxIndexBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(decompressed) > maxIndexBytes {
		return nil, errors.New("decompressed repository index exceeds maximum size")
	}
	return decompressed, nil
}

func collectProtoExtensionList(number int, wireType uint64, value []byte, entries *[]repoIndexEntry) error {
	if number != 101 || wireType != 2 {
		return nil
	}
	return walkProtoFields(value, func(number int, wireType uint64, value []byte, _ uint64) error {
		return collectProtoExtension(number, wireType, value, entries)
	})
}

func collectProtoExtension(number int, wireType uint64, value []byte, entries *[]repoIndexEntry) error {
	if number != 1 || wireType != 2 {
		return nil
	}
	entry, err := parseProtoRepoEntry(value)
	if err != nil {
		return err
	}
	*entries = append(*entries, entry)
	return nil
}

func parseProtoRepoEntry(data []byte) (repoIndexEntry, error) {
	var entry repoIndexEntry
	err := walkProtoFields(data, func(number int, wireType uint64, value []byte, varint uint64) error {
		switch {
		case number == 2 && wireType == 2:
			entry.Pkg = string(value)
		case number == 3 && wireType == 2:
			return walkProtoFields(value, func(number int, wireType uint64, value []byte, _ uint64) error {
				if number == 1 && wireType == 2 {
					entry.APKURL = string(value)
				}
				return nil
			})
		case number == 5 && wireType == 0:
			versionCode, err := checkedProtoInt(varint)
			if err != nil {
				return fmt.Errorf("repository version code %d overflows int", varint)
			}
			entry.Code = versionCode
		}
		return nil
	})
	return entry, err
}

// walkProtoFields is the small wire-format reader needed by the repository
// schema. It accepts and skips every non-group protobuf wire type, so unknown
// fields remain forward-compatible without generated Go bindings.
func walkProtoFields(data []byte, visit func(number int, wireType uint64, value []byte, varint uint64) error) error {
	for len(data) > 0 {
		field, remaining, err := consumeProtoField(data)
		if err != nil {
			return err
		}
		data = remaining
		if err := visit(field.number, field.wireType, field.value, field.scalar); err != nil {
			return err
		}
	}
	return nil
}

type protoField struct {
	number   int
	wireType uint64
	value    []byte
	scalar   uint64
}

func consumeProtoField(data []byte) (protoField, []byte, error) {
	key, width := binary.Uvarint(data)
	if width <= 0 {
		return protoField{}, nil, errors.New("invalid protobuf field key")
	}
	number, err := checkedProtoInt(key >> 3)
	if err != nil || number < 1 {
		return protoField{}, nil, errors.New("invalid protobuf field number")
	}
	field := protoField{number: number, wireType: key & 7}
	remaining, err := consumeProtoValue(data[width:], &field)
	return field, remaining, err
}

func consumeProtoValue(data []byte, field *protoField) ([]byte, error) {
	switch field.wireType {
	case 0:
		value, width := binary.Uvarint(data)
		if width <= 0 {
			return nil, errors.New("invalid protobuf varint")
		}
		field.scalar = value
		return data[width:], nil
	case 1:
		return consumeProtoBytes(data, 8, "truncated protobuf fixed64", field)
	case 2:
		length, width := binary.Uvarint(data)
		if width <= 0 {
			return nil, errors.New("invalid protobuf length")
		}
		size, err := checkedProtoInt(length)
		if err != nil || size > len(data[width:]) {
			return nil, errors.New("truncated protobuf bytes")
		}
		return consumeProtoBytes(data[width:], size, "truncated protobuf bytes", field)
	case 5:
		return consumeProtoBytes(data, 4, "truncated protobuf fixed32", field)
	default:
		return nil, fmt.Errorf("unsupported protobuf wire type %d", field.wireType)
	}
}

func consumeProtoBytes(data []byte, size int, truncated string, field *protoField) ([]byte, error) {
	if len(data) < size {
		return nil, errors.New(truncated)
	}
	field.value = data[:size]
	return data[size:], nil
}

func checkedProtoInt(value uint64) (int, error) {
	if value > math.MaxInt {
		return 0, errors.New("protobuf integer overflows int")
	}
	// #nosec G115 -- the architecture-sized upper bound is checked above.
	return int(value), nil
}

// repoBaseURL normalises a stored repo URL to its base DIRECTORY — the parent the
// index + apk paths hang off — regardless of whether the engine stored the bare
// repo directory or a URL pointing straight at the repo's INDEX FILE.
//
// The engine stores whatever repo URL the owner (or a default) configured, and the
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
func repoBaseURL(repoURL string) string {
	trimmed := strings.TrimRight(repoURL, "/")
	slash := strings.LastIndex(trimmed, "/")
	if slash < 0 {
		return trimmed
	}
	if namesIndexFile(trimmed) {
		return trimmed[:slash]
	}
	return trimmed
}

// indexURLFor preserves a configured index-file URL verbatim; changing its file
// name can change both schema and catalogue. A bare directory keeps the legacy
// default of "<base>/index.min.json".
func indexURLFor(repoURL string) string {
	trimmed := strings.TrimRight(repoURL, "/")
	if namesIndexFile(trimmed) {
		return trimmed
	}
	return trimmed + "/index.min.json"
}

func namesIndexFile(repoURL string) bool {
	slash := strings.LastIndex(repoURL, "/")
	last := strings.ToLower(repoURL[slash+1:])
	return strings.HasSuffix(last, ".json") || strings.HasSuffix(last, ".pb")
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
