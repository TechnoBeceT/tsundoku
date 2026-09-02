package enginetopo_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

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
