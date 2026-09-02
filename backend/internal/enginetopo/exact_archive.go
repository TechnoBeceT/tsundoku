package enginetopo

import (
	"context"
	"fmt"
	"io"
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

	apk, err := sourceengine.InstalledAPKFor(ctx, a.client, ext.PkgName)
	if err != nil {
		return fmt.Errorf("enginetopo: export installed apk %q: %w", ext.PkgName, err)
	}
	defer func() { _ = apk.Body.Close() }()
	if apk.PkgName != ext.PkgName || apk.VersionCode != int(ext.VersionCode) {
		return fmt.Errorf("enginetopo: exported generation %q@%d does not match installed %q@%d", apk.PkgName, apk.VersionCode, ext.PkgName, ext.VersionCode)
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
