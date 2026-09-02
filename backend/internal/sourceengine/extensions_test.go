package sourceengine_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

func TestInstalledAPK_StreamsExactAuthenticatedGeneration(t *testing.T) {
	const pkg = "eu.kanade.tachiyomi.extension.en.mangadex"
	want := []byte("exact-installed-apk")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/extensions/"+pkg+"/installed-apk" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer control-secret" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/vnd.android.package-archive")
		w.Header().Set("Content-Length", strconv.Itoa(len(want)))
		w.Header().Set("X-Tsundoku-Extension-Package", pkg)
		w.Header().Set("X-Tsundoku-Extension-Version-Code", "57")
		w.Header().Set("X-Tsundoku-Extension-Version-Name", "1.2.3")
		_, _ = w.Write(want)
	}))
	defer srv.Close()

	client := sourceengine.New(srv.URL, srv.Client(), "control-secret")
	apk, err := sourceengine.InstalledAPKFor(context.Background(), client, pkg)
	if err != nil {
		t.Fatalf("InstalledAPKFor: %v", err)
	}
	defer func() { _ = apk.Body.Close() }()
	got, err := io.ReadAll(apk.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if apk.PkgName != pkg || apk.VersionCode != 57 || apk.VersionName != "1.2.3" || apk.ContentLength != int64(len(want)) || !bytes.Equal(got, want) {
		t.Fatalf("apk = %+v bytes=%q", apk, got)
	}
}

func TestInstalledAPK_RejectsMismatchedIdentityAndClosesBody(t *testing.T) {
	closed := false
	doer := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
			"Content-Type":                      []string{"application/vnd.android.package-archive"},
			"Content-Length":                    []string{"3"},
			"X-Tsundoku-Extension-Package":      []string{"pkg.other"},
			"X-Tsundoku-Extension-Version-Code": []string{"1"},
		}, Body: &closeTrackingReader{Reader: bytes.NewReader([]byte("apk")), closed: &closed}}, nil
	})
	client := sourceengine.New("http://engine", doer, "secret")
	if _, err := sourceengine.InstalledAPKFor(context.Background(), client, "pkg.requested"); err == nil {
		t.Fatal("want identity error")
	}
	if !closed {
		t.Fatal("response body not closed after validation failure")
	}
}

func TestInstalledAPK_EscapesPackagePathAndScopesControlBearer(t *testing.T) {
	const pkg = "pkg with space"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/extensions":
			if got := r.Header.Get("Authorization"); got != "" {
				t.Fatalf("bearer leaked to ordinary route: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		case "/extensions/pkg with space/installed-apk":
			if r.RequestURI != "/extensions/pkg%20with%20space/installed-apk" {
				t.Fatalf("RequestURI=%q", r.RequestURI)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer secret" {
				t.Fatalf("Authorization=%q", got)
			}
			w.Header().Set("Content-Type", "application/vnd.android.package-archive")
			w.Header().Set("X-Tsundoku-Extension-Package", pkg)
			w.Header().Set("X-Tsundoku-Extension-Version-Code", "1")
			w.Header().Set("Content-Length", "3")
			_, _ = w.Write([]byte("APK"))
		default:
			t.Fatalf("path=%q", r.URL.Path)
		}
	}))
	defer srv.Close()
	client := sourceengine.New(srv.URL, srv.Client(), "secret")
	if _, err := client.Extensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	apk, err := sourceengine.InstalledAPKFor(context.Background(), client, pkg)
	if err != nil {
		t.Fatal(err)
	}
	_ = apk.Body.Close()
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestInstalledAPK_CancellationInterruptsStreamAndBodyCloses(t *testing.T) {
	const pkg = "pkg.one"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.android.package-archive")
		w.Header().Set("X-Tsundoku-Extension-Package", pkg)
		w.Header().Set("X-Tsundoku-Extension-Version-Code", "1")
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	apk, err := sourceengine.InstalledAPKFor(ctx, sourceengine.New(srv.URL, srv.Client(), "secret"), pkg)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := io.ReadAll(apk.Body); err == nil {
		t.Fatal("want cancellation read error")
	}
	if err := apk.Body.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type closeTrackingReader struct {
	io.Reader
	closed *bool
}

func (r *closeTrackingReader) Close() error { *r.closed = true; return nil }

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

// assertExtensionListCall drives one body-less extension endpoint end to end: it
// stands up a server pinning the METHOD and PATH the client must hit, then proves
// the plain-array response decodes into wantExtensions().
//
// Four of the five extension endpoints differ ONLY in verb, path and which client
// method is called — everything else (the canned body, the decode, the compare) is
// identical. Sharing the scaffolding keeps those three distinguishing facts on one
// visible line per test and stops the four copies drifting in how strictly they
// check. Install is deliberately NOT routed through here: it asserts on the
// REQUEST BODY the client sent, which is its whole point.
func assertExtensionListCall(t *testing.T, wantMethod, wantPath string, call func(sourceengine.Client) ([]sourceengine.Extension, error)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != wantMethod || r.URL.Path != wantPath {
			t.Errorf("unexpected request: %s %s, want %s %s", r.Method, r.URL.Path, wantMethod, wantPath)
		}
		writeJSON(t, w, http.StatusOK, extensionsResponseBody())
	}))
	defer srv.Close()

	got, err := call(newTestClient(t, srv))
	if err != nil {
		t.Fatalf("%s %s: %v", wantMethod, wantPath, err)
	}
	if !reflect.DeepEqual(got, wantExtensions()) {
		t.Errorf("%s %s = %+v, want %+v", wantMethod, wantPath, got, wantExtensions())
	}
}

// TestExtensions_Success proves GET /extensions decodes the plain array.
func TestExtensions_Success(t *testing.T) {
	assertExtensionListCall(t, http.MethodGet, "/extensions", func(c sourceengine.Client) ([]sourceengine.Extension, error) {
		return c.Extensions(context.Background())
	})
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
	assertExtensionListCall(t, http.MethodPost, "/extensions/refresh", func(c sourceengine.Client) ([]sourceengine.Extension, error) {
		return c.RefreshExtensions(context.Background())
	})
}

// TestUpdateExtension_Success proves POST /extensions/{pkg}/update targets
// the correct path and returns the refreshed list.
func TestUpdateExtension_Success(t *testing.T) {
	const pkg = "eu.kanade.tachiyomi.extension.en.mangadex"
	assertExtensionListCall(t, http.MethodPost, "/extensions/"+pkg+"/update", func(c sourceengine.Client) ([]sourceengine.Extension, error) {
		return c.UpdateExtension(context.Background(), pkg)
	})
}

// TestUninstallExtension_Success proves DELETE /extensions/{pkg} targets the
// correct path and returns the refreshed list.
func TestUninstallExtension_Success(t *testing.T) {
	const pkg = "eu.kanade.tachiyomi.extension.en.mangadex"
	assertExtensionListCall(t, http.MethodDelete, "/extensions/"+pkg, func(c sourceengine.Client) ([]sourceengine.Extension, error) {
		return c.UninstallExtension(context.Background(), pkg)
	})
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
