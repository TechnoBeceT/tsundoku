package extensions

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

func TestUpdate_CommitFailureAfterActivationPreservesSuccessAndPostArchive(t *testing.T) {
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	before := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 1, VersionName: "v1", IsInstalled: true, Sources: []sourceengine.Source{{ID: 11}}}
	after := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 2, VersionName: "v2", IsInstalled: true, Sources: []sourceengine.Source{{ID: 11}}}
	client := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{before}), sourceenginefake.WithUpdateExtensions([]sourceengine.Extension{after}), sourceenginefake.WithInstalledAPK(before.PkgName, 1, "v1", []byte("APK-1")), sourceenginefake.WithInstalledAPK(after.PkgName, 2, "v2", []byte("APK-2")))
	h := NewHandler(client, db, cache, nil, nil, nil, nil).WithArchive(enginetopo.NewExtensionArchive(client, db, cache, nil))
	h.beforeUpdateCommit = func(tx *ent.Tx) {
		tx.OnCommit(func(ent.Committer) ent.Committer {
			return ent.CommitFunc(func(context.Context, *ent.Tx) error { return errors.New("injected commit failure") })
		})
	}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/suwayomi/extensions/:pkgName/update")
	c.SetParamNames("pkgName")
	c.SetParamValues(before.PkgName)
	if err := h.Update(c); err != nil {
		e.HTTPErrorHandler(err, c)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if client.CallCount("ActivatePreparedExtensionUpdate") != 1 || client.CallCount("InstalledAPK") != 2 || client.CallCount("DiscardPreparedExtensionUpdate") != 0 {
		t.Fatalf("activate=%d archive=%d discard=%d", client.CallCount("ActivatePreparedExtensionUpdate"), client.CallCount("InstalledAPK"), client.CallCount("DiscardPreparedExtensionUpdate"))
	}
	if !cache.Exists(before.PkgName, 2) {
		t.Fatal("activated generation was not archived after commit degradation")
	}
}
