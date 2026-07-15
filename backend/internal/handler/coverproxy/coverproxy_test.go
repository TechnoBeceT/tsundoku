package coverproxy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/coverproxy"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// fakeClient is a minimal suwayomi.Client stub. Only PageBytes is exercised;
// every other method returns a zero value so the interface can be satisfied
// without noise (mirrors the fakes in handler/series and handler/imports).
type fakeClient struct {
	pageBytes func(ctx context.Context, url string) ([]byte, string, error)
}

func (f *fakeClient) Sources(context.Context) ([]suwayomi.Source, error) { return nil, nil }
func (f *fakeClient) Search(context.Context, string, string) ([]suwayomi.Manga, error) {
	return nil, nil
}
func (f *fakeClient) Browse(context.Context, string, suwayomi.BrowseType, int) (suwayomi.BrowseResult, error) {
	return suwayomi.BrowseResult{}, nil
}
func (f *fakeClient) FetchChapters(context.Context, int) ([]suwayomi.Chapter, error) {
	return nil, nil
}
func (f *fakeClient) MangaChapters(context.Context, int) ([]suwayomi.Chapter, error) {
	return nil, nil
}
func (f *fakeClient) ChapterPages(context.Context, int) ([]string, error) { return nil, nil }
func (f *fakeClient) MangaMeta(context.Context, int) (suwayomi.Manga, error) {
	return suwayomi.Manga{}, nil
}
func (f *fakeClient) FetchMangaDetails(context.Context, int) (suwayomi.Manga, error) {
	return suwayomi.Manga{}, nil
}
func (f *fakeClient) PageBytes(ctx context.Context, pageURL string) ([]byte, string, error) {
	if f.pageBytes != nil {
		return f.pageBytes(ctx, pageURL)
	}
	return nil, "", errors.New("PageBytes: not configured")
}
func (f *fakeClient) ServerSettings(context.Context) (suwayomi.SuwayomiSettings, error) {
	return suwayomi.SuwayomiSettings{}, nil
}
func (f *fakeClient) SetServerSettings(context.Context, suwayomi.SuwayomiSettingsPatch) error {
	return nil
}
func (f *fakeClient) Extensions(context.Context) ([]suwayomi.Extension, error) { return nil, nil }
func (f *fakeClient) SetExtensionState(context.Context, string, suwayomi.ExtensionAction) error {
	return nil
}
func (f *fakeClient) FetchExtensions(context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (f *fakeClient) ExtensionRepos(context.Context) ([]string, error)  { return nil, nil }
func (f *fakeClient) SetExtensionRepos(context.Context, []string) error { return nil }
func (f *fakeClient) SourcePreferences(context.Context, string) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (f *fakeClient) SetSourcePreference(context.Context, string, int, suwayomi.PreferenceValue) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (f *fakeClient) ExtensionSources(context.Context, string) ([]suwayomi.Source, error) {
	return nil, nil
}
func (f *fakeClient) SetSourceEnabled(context.Context, string, bool) error { return nil }

// newTestContext builds a bare echo.Context over a GET request/response pair.
func newTestContext() (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

// TestStream_OK verifies Stream writes the fetched bytes with a Content-Type
// resolved from the reported extension.
func TestStream_OK(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47}
	sw := &fakeClient{pageBytes: func(_ context.Context, _ string) ([]byte, string, error) {
		return pngBytes, "png", nil
	}}
	c, rec := newTestContext()

	if err := coverproxy.Stream(c, sw, "/api/v1/manga/1/thumbnail"); err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("Stream: status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Stream: Content-Type = %q, want image/png", ct)
	}
	if rec.Body.String() != string(pngBytes) {
		t.Errorf("Stream: body mismatch")
	}
}

// TestStream_UnknownExtFallsBackToOctetStream verifies an unrecognised
// extension maps to application/octet-stream rather than an empty type.
func TestStream_UnknownExtFallsBackToOctetStream(t *testing.T) {
	sw := &fakeClient{pageBytes: func(_ context.Context, _ string) ([]byte, string, error) {
		return []byte("data"), "bin", nil
	}}
	c, rec := newTestContext()

	if err := coverproxy.Stream(c, sw, "/x"); err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Stream: Content-Type = %q, want application/octet-stream", ct)
	}
}

// TestStream_PageBytesFail verifies a Suwayomi fetch failure maps to 502, not
// a false 200 (the upstream is a separate service — never lie about success).
func TestStream_PageBytesFail(t *testing.T) {
	sw := &fakeClient{pageBytes: func(_ context.Context, _ string) ([]byte, string, error) {
		return nil, "", errors.New("suwayomi down")
	}}
	c, _ := newTestContext()

	err := coverproxy.Stream(c, sw, "/x")
	var he *echo.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("Stream: want *echo.HTTPError, got %T (%v)", err, err)
	}
	if he.Code != http.StatusBadGateway {
		t.Errorf("Stream: code = %d, want 502", he.Code)
	}
}

// TestStreamEngine_OK verifies StreamEngine calls Image with pageURL EMPTY and
// the cover URL in the imageURL slot, and writes the fetched bytes with a
// Content-Type resolved from the reported extension.
func TestStreamEngine_OK(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47}
	const sourceID int64 = 7
	const coverURL = "https://source.example/covers/1.jpg"
	engine := sourceenginefake.New(sourceenginefake.WithCoverImage(sourceID, coverURL, pngBytes, "png"))
	c, rec := newTestContext()

	if err := coverproxy.StreamEngine(c, engine, sourceID, coverURL); err != nil {
		t.Fatalf("StreamEngine: unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("StreamEngine: status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("StreamEngine: Content-Type = %q, want image/png", ct)
	}
	if rec.Body.String() != string(pngBytes) {
		t.Errorf("StreamEngine: body mismatch")
	}
	if got := engine.CallCount("Image"); got != 1 {
		t.Errorf("StreamEngine: Image call count = %d, want 1", got)
	}
}

// TestStreamEngine_UnknownExtFallsBackToOctetStream verifies an unrecognised
// extension maps to application/octet-stream rather than an empty type.
func TestStreamEngine_UnknownExtFallsBackToOctetStream(t *testing.T) {
	const sourceID int64 = 1
	const coverURL = "/x"
	engine := sourceenginefake.New(sourceenginefake.WithCoverImage(sourceID, coverURL, []byte("data"), "bin"))
	c, rec := newTestContext()

	if err := coverproxy.StreamEngine(c, engine, sourceID, coverURL); err != nil {
		t.Fatalf("StreamEngine: unexpected error: %v", err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("StreamEngine: Content-Type = %q, want application/octet-stream", ct)
	}
}

// TestStreamEngine_ImageFail verifies an engine-host fetch failure maps to
// 502, not a false 200 (the upstream is a separate service — never lie about
// success).
func TestStreamEngine_ImageFail(t *testing.T) {
	engine := sourceenginefake.New(sourceenginefake.WithError("Image", errors.New("engine down")))
	c, _ := newTestContext()

	err := coverproxy.StreamEngine(c, engine, 1, "/x")
	var he *echo.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("StreamEngine: want *echo.HTTPError, got %T (%v)", err, err)
	}
	if he.Code != http.StatusBadGateway {
		t.Errorf("StreamEngine: code = %d, want 502", he.Code)
	}
}

// TestStreamEngine_PageURLAlwaysEmpty proves StreamEngine calls Image with
// pageURL="" — the cover-fetch shape (imageURL carries the address, mirroring
// series.fetchAndCacheCover) — by seeding a page-keyed (non-cover) entry under
// the same sourceID/URL and confirming it is NEVER returned for a cover fetch.
func TestStreamEngine_PageURLAlwaysEmpty(t *testing.T) {
	const sourceID int64 = 3
	const url = "https://source.example/covers/2.jpg"
	// WithImage keys on (sourceID, pageURL) — seeding it here must NOT satisfy a
	// StreamEngine (cover) call, which always passes pageURL="".
	engine := sourceenginefake.New(
		sourceenginefake.WithImage(sourceID, url, []byte("PAGE-BYTES"), "jpg"),
		sourceenginefake.WithCoverImage(sourceID, url, []byte("COVER-BYTES"), "png"),
	)
	c, rec := newTestContext()

	if err := coverproxy.StreamEngine(c, engine, sourceID, url); err != nil {
		t.Fatalf("StreamEngine: unexpected error: %v", err)
	}
	if rec.Body.String() != "COVER-BYTES" {
		t.Fatalf("StreamEngine: body = %q, want COVER-BYTES (must not collide with the page-keyed entry)", rec.Body.String())
	}
}
