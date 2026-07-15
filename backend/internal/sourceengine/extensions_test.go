package sourceengine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// extensionsResponseBody is the canned plain-array body every
// extension-listing endpoint returns.
func extensionsResponseBody() []map[string]any {
	return []map[string]any{
		{
			"pkgName": "eu.kanade.tachiyomi.extension.en.mangadex",
			"name":    "MangaDex", "versionName": "1.4.2", "versionCode": int64(14),
			"lang": "en", "isInstalled": true, "hasUpdate": false, "isNsfw": false,
			"iconUrl": "https://x/icon.png", "repoUrl": nil,
			"sources": []map[string]any{{"id": 1, "name": "MangaDex", "lang": "en"}},
		},
	}
}

func wantExtensions() []sourceengine.Extension {
	return []sourceengine.Extension{
		{
			PkgName: "eu.kanade.tachiyomi.extension.en.mangadex",
			Name:    "MangaDex", VersionName: "1.4.2", VersionCode: 14,
			Lang: "en", IsInstalled: true, HasUpdate: false, IsNsfw: false,
			IconURL: "https://x/icon.png", RepoURL: nil,
			Sources: []sourceengine.Source{{ID: 1, Name: "MangaDex", Lang: "en"}},
		},
	}
}

// TestExtensions_Success proves GET /extensions decodes the plain array.
func TestExtensions_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/extensions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, extensionsResponseBody())
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Extensions(context.Background())
	if err != nil {
		t.Fatalf("Extensions: %v", err)
	}
	if !reflect.DeepEqual(got, wantExtensions()) {
		t.Errorf("Extensions = %+v, want %+v", got, wantExtensions())
	}
}

// TestInstallExtension_Success proves POST /extensions/install sends only the
// non-empty identifier (pkgName here) and returns the refreshed list.
func TestInstallExtension_Success(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/extensions/install" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		decodeBody(t, r, &captured)
		writeJSON(t, w, http.StatusOK, extensionsResponseBody())
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).InstallExtension(context.Background(), "eu.kanade.tachiyomi.extension.en.mangadex", "")
	if err != nil {
		t.Fatalf("InstallExtension: %v", err)
	}
	if !reflect.DeepEqual(got, wantExtensions()) {
		t.Errorf("InstallExtension = %+v, want %+v", got, wantExtensions())
	}
	if captured["pkgName"] != "eu.kanade.tachiyomi.extension.en.mangadex" {
		t.Errorf("request body pkgName = %v", captured["pkgName"])
	}
	if _, ok := captured["apkUrl"]; ok {
		t.Errorf("apkUrl must be omitted when empty, got %+v", captured)
	}
}

// TestInstallExtension_ByApkURL proves the apkUrl-only install path sends
// only apkUrl and omits pkgName.
func TestInstallExtension_ByApkURL(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeBody(t, r, &captured)
		writeJSON(t, w, http.StatusOK, extensionsResponseBody())
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).InstallExtension(context.Background(), "", "https://x/ext.apk")
	if err != nil {
		t.Fatalf("InstallExtension: %v", err)
	}
	if captured["apkUrl"] != "https://x/ext.apk" {
		t.Errorf("request body apkUrl = %v", captured["apkUrl"])
	}
	if _, ok := captured["pkgName"]; ok {
		t.Errorf("pkgName must be omitted when empty, got %+v", captured)
	}
}

// TestRefreshExtensions_Success proves POST /extensions/refresh returns the
// refreshed list.
func TestRefreshExtensions_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/extensions/refresh" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, extensionsResponseBody())
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).RefreshExtensions(context.Background())
	if err != nil {
		t.Fatalf("RefreshExtensions: %v", err)
	}
	if !reflect.DeepEqual(got, wantExtensions()) {
		t.Errorf("RefreshExtensions = %+v, want %+v", got, wantExtensions())
	}
}

// TestUpdateExtension_Success proves POST /extensions/{pkg}/update targets
// the correct path and returns the refreshed list.
func TestUpdateExtension_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/extensions/eu.kanade.tachiyomi.extension.en.mangadex/update"
		if r.Method != http.MethodPost || r.URL.Path != wantPath {
			t.Errorf("unexpected request: %s %s, want POST %s", r.Method, r.URL.Path, wantPath)
		}
		writeJSON(t, w, http.StatusOK, extensionsResponseBody())
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).UpdateExtension(context.Background(), "eu.kanade.tachiyomi.extension.en.mangadex")
	if err != nil {
		t.Fatalf("UpdateExtension: %v", err)
	}
	if !reflect.DeepEqual(got, wantExtensions()) {
		t.Errorf("UpdateExtension = %+v, want %+v", got, wantExtensions())
	}
}

// TestUninstallExtension_Success proves DELETE /extensions/{pkg} targets the
// correct path and returns the refreshed list.
func TestUninstallExtension_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/extensions/eu.kanade.tachiyomi.extension.en.mangadex"
		if r.Method != http.MethodDelete || r.URL.Path != wantPath {
			t.Errorf("unexpected request: %s %s, want DELETE %s", r.Method, r.URL.Path, wantPath)
		}
		writeJSON(t, w, http.StatusOK, extensionsResponseBody())
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).UninstallExtension(context.Background(), "eu.kanade.tachiyomi.extension.en.mangadex")
	if err != nil {
		t.Fatalf("UninstallExtension: %v", err)
	}
	if !reflect.DeepEqual(got, wantExtensions()) {
		t.Errorf("UninstallExtension = %+v, want %+v", got, wantExtensions())
	}
}

// TestExtensions_BadRequest proves a 400 maps to *BadRequestError.
func TestExtensions_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "invalid pkgName in path"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).UpdateExtension(context.Background(), "bogus")
	assertBadRequestError(t, err)
}

// TestExtensions_UpstreamFailure proves a 502 maps to *UpstreamError.
func TestExtensions_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "boom"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Extensions(context.Background())
	assertUpstreamError(t, err, http.StatusBadGateway)
}
