package enginetopo

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// ExtensionArchive serializes exact installed-generation capture across boot
// seeding and live extension updates. One instance is shared by both paths so
// cache pruning and HarvestedExtension metadata cannot interleave.
type ExtensionArchive struct {
	mu       sync.Mutex
	client   sourceengine.Client
	db       *ent.Client
	cache    *apkcache.Store
	retained func(context.Context) int
}

func NewExtensionArchive(client sourceengine.Client, db *ent.Client, cache *apkcache.Store, retained func(context.Context) int) *ExtensionArchive {
	return &ExtensionArchive{client: client, db: db, cache: cache, retained: retained}
}

// Capture durably records exactly the generation the engine reports installed.
func (a *ExtensionArchive) Capture(ctx context.Context, ext sourceengine.Extension) error {
	if a == nil || a.client == nil || a.db == nil || a.cache == nil {
		return fmt.Errorf("enginetopo: exact extension archive unavailable")
	}
	if !ext.IsInstalled || ext.PkgName == "" || ext.VersionCode <= 0 {
		return fmt.Errorf("enginetopo: extension %q is not an installed generation", ext.PkgName)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.captureLocked(ctx, ext)
}

func (a *ExtensionArchive) captureLocked(ctx context.Context, ext sourceengine.Extension) error {
	apk, err := sourceengine.InstalledAPKFor(ctx, a.client, ext.PkgName)
	if err != nil {
		return fmt.Errorf("enginetopo: export installed apk %q: %w", ext.PkgName, err)
	}
	defer func() { _ = apk.Body.Close() }()
	if apk.PkgName != ext.PkgName || apk.VersionCode != int(ext.VersionCode) {
		return fmt.Errorf("enginetopo: exported generation %q@%d does not match installed %q@%d", apk.PkgName, apk.VersionCode, ext.PkgName, ext.VersionCode)
	}
	if apk.ContentLength <= 0 || apk.ContentLength > maxAPKBytes {
		return fmt.Errorf("enginetopo: exported extension %q has invalid content length %d", ext.PkgName, apk.ContentLength)
	}
	sha, _, err := a.cache.Put(ext.PkgName, apk.VersionCode, &exactLengthReader{r: apk.Body, remaining: apk.ContentLength})
	if err != nil {
		return fmt.Errorf("enginetopo: cache exact installed apk: %w", err)
	}
	retained := resolveRetained(ctx, a.retained)
	cachedVersions := pruneAndBuildCachedVersions(ctx, a.db, a.cache, ext.PkgName, retained, apk.VersionCode, apk.VersionName, apk.VersionCode)
	if err := upsertExtension(ctx, a.db, extensionRow{
		pkgName: ext.PkgName, repoURL: repoURLOf(ext), versionCode: apk.VersionCode,
		installedVersionCode: apk.VersionCode, versionName: apk.VersionName,
		sourceIDs: sourceIDs(ext.Sources), apkSHA256: sha, apkCached: true,
		cachedVersions: cachedVersions,
	}); err != nil {
		return fmt.Errorf("enginetopo: persist exact installed extension: %w", err)
	}
	return nil
}

// Update serializes the complete rollback-safe update lifecycle. mutated is
// true only after UpdateExtension returned success; callers must preserve that
// success even if the post-mutation archive reports degradation.
func (a *ExtensionArchive) Update(ctx context.Context, pkgName string) (exts []sourceengine.Extension, mutated bool, err error) {
	if a == nil || a.client == nil || a.db == nil || a.cache == nil {
		return nil, false, fmt.Errorf("enginetopo: exact extension archive unavailable")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	before, err := a.client.Extensions(ctx)
	if err != nil {
		return nil, false, err
	}
	installed, ok := extensionByPackage(before, pkgName)
	if !ok || !installed.IsInstalled {
		return nil, false, fmt.Errorf("installed extension %q not found", pkgName)
	}
	if err := a.captureLocked(ctx, installed); err != nil {
		return nil, false, err
	}
	exts, err = a.client.UpdateExtension(ctx, pkgName)
	if err != nil {
		return nil, false, err
	}
	updated, ok := extensionByPackage(exts, pkgName)
	if !ok || !updated.IsInstalled {
		return exts, true, fmt.Errorf("updated extension %q missing from engine response", pkgName)
	}
	if err := a.captureLocked(ctx, updated); err != nil {
		return exts, true, err
	}
	return exts, true, nil
}

// SeedInstalled serializes the complete boot capture decision with live
// updates: installed-state snapshot, cache check, capture, and gap persistence.
func (a *ExtensionArchive) SeedInstalled(ctx context.Context) (cached, gaps int, err error) {
	if a == nil || a.client == nil || a.db == nil || a.cache == nil {
		return 0, 0, fmt.Errorf("enginetopo: exact extension archive unavailable")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exts, err := a.client.Extensions(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, ext := range exts {
		if !ext.IsInstalled || exactExtensionCached(ctx, a.db, a.cache, ext) {
			continue
		}
		if err := a.captureLocked(ctx, ext); err != nil {
			slog.WarnContext(ctx, "enginetopo: could not archive exact installed extension", "pkg_name", ext.PkgName, "version_code", ext.VersionCode, "err", err)
			recordGap(ctx, a.db, ext)
			gaps++
			continue
		}
		cached++
	}
	return cached, gaps, nil
}

func extensionByPackage(exts []sourceengine.Extension, pkgName string) (sourceengine.Extension, bool) {
	for _, ext := range exts {
		if ext.PkgName == pkgName {
			return ext, true
		}
	}
	return sourceengine.Extension{}, false
}

type exactLengthReader struct {
	r         io.Reader
	remaining int64
	checked   bool
}

func (r *exactLengthReader) Read(p []byte) (int, error) {
	if r.remaining > 0 {
		if int64(len(p)) > r.remaining {
			p = p[:r.remaining]
		}
		n, err := r.r.Read(p)
		r.remaining -= int64(n)
		if err == io.EOF && r.remaining > 0 {
			return n, io.ErrUnexpectedEOF
		}
		return n, err
	}
	if r.checked {
		return 0, io.EOF
	}
	r.checked = true
	var one [1]byte
	n, err := r.r.Read(one[:])
	if n > 0 {
		return 0, fmt.Errorf("installed APK exceeds declared content length")
	}
	if err != nil && err != io.EOF {
		return 0, err
	}
	return 0, io.EOF
}
