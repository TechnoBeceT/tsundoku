package enginetopo_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

type blockingLifecycleClient struct {
	*sourceenginefake.Client
	entered chan struct{}
	release chan struct{}
}

type malformedAPKClient struct {
	*sourceenginefake.Client
	apk sourceengine.InstalledAPK
}

func (c *malformedAPKClient) InstalledAPK(context.Context, string) (sourceengine.InstalledAPK, error) {
	return c.apk, nil
}

func (c *blockingLifecycleClient) UpdateExtension(ctx context.Context, pkg string) ([]sourceengine.Extension, error) {
	c.entered <- struct{}{}
	select {
	case <-c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return c.Client.UpdateExtension(ctx, pkg)
}

func TestExtensionArchiveCaptureStoresExactInstalledGeneration(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	repo := "https://repo.test/index.json"
	ext := sourceengine.Extension{PkgName: "pkg.one", VersionCode: 57, VersionName: "1.57", RepoURL: &repo, IsInstalled: true, Sources: []sourceengine.Source{{ID: 8}}}
	client := sourceenginefake.New(sourceenginefake.WithInstalledAPK(ext.PkgName, 57, "1.57", []byte("EXACT-57")))
	archive := enginetopo.NewExtensionArchive(client, db, cache, func(context.Context) int { return 3 })

	if err := archive.Capture(ctx, ext); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	row := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName(ext.PkgName)).OnlyX(ctx)
	if row.VersionCode != 57 || row.InstalledVersionCode != 57 || !row.ApkCached || len(row.CachedVersions) != 1 || row.CachedVersions[0].VersionCode != 57 {
		t.Fatalf("row = %+v", row)
	}
	r, err := cache.Open(ext.PkgName, 57)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, []byte("EXACT-57")) {
		t.Fatalf("bytes = %q", got)
	}
}

func TestSeedExtensionsExactUsesInstalledExportWithoutRepositoryAPK(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	repo := "https://repo.test/index.json"
	ext := sourceengine.Extension{PkgName: "pkg.legacy", VersionCode: 57, VersionName: "1.57", RepoURL: &repo, IsInstalled: true}
	client := sourceenginefake.New(
		sourceenginefake.WithRepos([]string{repo}),
		sourceenginefake.WithExtensions([]sourceengine.Extension{ext}),
		sourceenginefake.WithInstalledAPK(ext.PkgName, 57, "1.57", []byte("EXACT-LEGACY-57")),
	)
	archive := enginetopo.NewExtensionArchive(client, db, cache, nil)
	res, err := enginetopo.SeedExtensionsExact(ctx, client, db, cache, archive)
	if err != nil {
		t.Fatalf("SeedExtensionsExact: %v", err)
	}
	if res.Cached != 1 || res.Gaps != 0 || !cache.Exists(ext.PkgName, 57) {
		t.Fatalf("result=%+v exactExists=%v", res, cache.Exists(ext.PkgName, 57))
	}
}

func TestExtensionArchiveUpdateSerializesWholeLifecycle(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	ext := sourceengine.Extension{PkgName: "pkg.one", VersionCode: 57, VersionName: "1.57", IsInstalled: true}
	base := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{ext}), sourceenginefake.WithInstalledAPK(ext.PkgName, 57, ext.VersionName, []byte("APK-57")))
	client := &blockingLifecycleClient{Client: base, entered: make(chan struct{}, 2), release: make(chan struct{}, 2)}
	archive := enginetopo.NewExtensionArchive(client, db, cache, nil)
	done := make(chan error, 2)
	go func() { _, _, err := archive.Update(ctx, ext.PkgName); done <- err }()
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		t.Fatal("first update did not enter")
	}
	go func() { _, _, err := archive.Update(ctx, ext.PkgName); done <- err }()
	select {
	case <-client.entered:
		t.Fatal("second update entered mutation while first lifecycle was in flight")
	case <-time.After(100 * time.Millisecond):
	}
	client.release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		t.Fatal("second update did not proceed after release")
	}
	client.release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSeedExtensionsExactSecondRunDoesNotReadAPKAgain(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	ext := sourceengine.Extension{PkgName: "pkg.one", VersionCode: 7, IsInstalled: true}
	client := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{ext}), sourceenginefake.WithInstalledAPK(ext.PkgName, 7, "v7", []byte("APK")))
	archive := enginetopo.NewExtensionArchive(client, db, cache, nil)
	if _, err := enginetopo.SeedExtensionsExact(ctx, client, db, cache, archive); err != nil {
		t.Fatal(err)
	}
	if _, err := enginetopo.SeedExtensionsExact(ctx, client, db, cache, archive); err != nil {
		t.Fatal(err)
	}
	if got := client.CallCount("InstalledAPK"); got != 1 {
		t.Fatalf("InstalledAPK calls=%d, want 1", got)
	}
}

func TestSeedExtensionsExactIsolatesOnePackageGap(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	good := sourceengine.Extension{PkgName: "pkg.good", VersionCode: 7, IsInstalled: true}
	bad := sourceengine.Extension{PkgName: "pkg.bad", VersionCode: 9, IsInstalled: true}
	client := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{bad, good}), sourceenginefake.WithInstalledAPK(good.PkgName, 7, "v7", []byte("GOOD")))
	res, err := enginetopo.SeedExtensionsExact(ctx, client, db, cache, enginetopo.NewExtensionArchive(client, db, cache, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Cached != 1 || res.Gaps != 1 || !cache.Exists(good.PkgName, 7) {
		t.Fatalf("res=%+v good=%v", res, cache.Exists(good.PkgName, 7))
	}
}

func TestExtensionArchiveMalformedStreamPreservesPreviousCacheAndMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		length int64
	}{
		{name: "truncated", length: 100},
		{name: "oversize", length: 257 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := testdb.New(t)
			cache := apkcache.New(t.TempDir())
			ext := sourceengine.Extension{PkgName: "pkg.one", VersionCode: 57, VersionName: "v57", IsInstalled: true}
			if _, _, err := cache.Put(ext.PkgName, 57, bytes.NewReader([]byte("PREVIOUS"))); err != nil {
				t.Fatal(err)
			}
			if err := db.HarvestedExtension.Create().SetPkgName(ext.PkgName).SetVersionCode(57).SetInstalledVersionCode(57).SetVersionName("v57").SetApkSha256("previous-sha").SetApkCached(true).SetCachedVersions([]apkcache.CachedVersion{{VersionCode: 57, VersionName: "v57"}}).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			base := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{ext}))
			client := &malformedAPKClient{Client: base, apk: sourceengine.InstalledAPK{PkgName: ext.PkgName, VersionCode: 57, VersionName: "v57", ContentLength: tc.length, Body: io.NopCloser(bytes.NewReader([]byte("SHORT")))}}
			if err := enginetopo.NewExtensionArchive(client, db, cache, nil).Capture(ctx, ext); err == nil {
				t.Fatal("want malformed stream error")
			}
			row := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName(ext.PkgName)).OnlyX(ctx)
			if row.ApkSha256 != "previous-sha" || row.VersionCode != 57 {
				t.Fatalf("metadata changed: %+v", row)
			}
			r, err := cache.Open(ext.PkgName, 57)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close() }()
			got, _ := io.ReadAll(r)
			if !bytes.Equal(got, []byte("PREVIOUS")) {
				t.Fatalf("cache=%q", got)
			}
		})
	}
}
