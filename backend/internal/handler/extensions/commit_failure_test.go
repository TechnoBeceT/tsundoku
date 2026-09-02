package extensions

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

type responseLossProtectedClient struct{ *sourceenginefake.Client }

func (c *responseLossProtectedClient) ActivatePreparedExtensionUpdate(ctx context.Context, req sourceengine.ActivatePreparedExtensionUpdate) ([]sourceengine.Extension, error) {
	if _, err := c.Client.ActivatePreparedExtensionUpdate(ctx, req); err != nil {
		return nil, err
	}
	return nil, errors.New("activation response lost after publication")
}

type ambiguousProtectedClient struct{ *sourceenginefake.Client }

func (c *ambiguousProtectedClient) ActivatePreparedExtensionUpdate(context.Context, sourceengine.ActivatePreparedExtensionUpdate) ([]sourceengine.Extension, error) {
	return nil, errors.New("activation connection lost")
}

func (c *ambiguousProtectedClient) PreparedExtensionUpdateOutcome(_ context.Context, pkgName, _ string) (sourceengine.PreparedExtensionUpdateOutcome, error) {
	return sourceengine.PreparedExtensionUpdateOutcome{Status: "pending", PkgName: pkgName, CandidateVersionCode: 2}, nil
}

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

func TestUpdate_ResponseLossAfterPublicationReconcilesCommittedOutcome(t *testing.T) {
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	before := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 1, VersionName: "v1", IsInstalled: true, Sources: []sourceengine.Source{{ID: 11}}}
	after := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 2, VersionName: "v2", IsInstalled: true, Sources: []sourceengine.Source{{ID: 11}}}
	base := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{before}), sourceenginefake.WithUpdateExtensions([]sourceengine.Extension{after}), sourceenginefake.WithInstalledAPK(before.PkgName, 1, "v1", []byte("APK-1")), sourceenginefake.WithInstalledAPK(after.PkgName, 2, "v2", []byte("APK-2")))
	client := &responseLossProtectedClient{Client: base}
	h := NewHandler(client, db, cache, nil, nil, nil, nil).WithArchive(enginetopo.NewExtensionArchive(client, db, cache, nil))
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
	if base.CallCount("PreparedExtensionUpdateOutcome") != 1 || base.CallCount("DiscardPreparedExtensionUpdate") != 0 || !cache.Exists(before.PkgName, 2) {
		t.Fatalf("outcome=%d discard=%d postArchive=%v", base.CallCount("PreparedExtensionUpdateOutcome"), base.CallCount("DiscardPreparedExtensionUpdate"), cache.Exists(before.PkgName, 2))
	}
}

func TestUpdate_AmbiguousActivationReturnsExplicitNonRetryResponse(t *testing.T) {
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	before := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 1, VersionName: "v1", IsInstalled: true}
	after := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 2, VersionName: "v2", IsInstalled: true}
	base := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{before}), sourceenginefake.WithUpdateExtensions([]sourceengine.Extension{after}), sourceenginefake.WithInstalledAPK(before.PkgName, 1, "v1", []byte("APK-1")))
	client := &ambiguousProtectedClient{Client: base}
	h := NewHandler(client, db, cache, nil, nil, nil, nil).WithArchive(enginetopo.NewExtensionArchive(client, db, cache, nil))
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
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "activation_outcome_ambiguous") || !strings.Contains(rec.Body.String(), "do not retry") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if base.CallCount("DiscardPreparedExtensionUpdate") != 0 {
		t.Fatal("ambiguous activation was discarded")
	}
}
