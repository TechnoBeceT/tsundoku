package sourceengine_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

func TestImage_StructuredUpstreamThrottle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]any{
			"error": "UpstreamHttpFailure: HTTP error 429", "upstreamStatus": 429, "retryAfterSeconds": 90,
		})
	}))
	defer srv.Close()

	_, _, err := newTestClient(t, srv).Image(context.Background(), 7, "/page/1", "https://images.test/1.jpg")
	var upstream *sourceengine.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("Image error = %v, want *UpstreamError", err)
	}
	if upstream.Status != http.StatusBadGateway || upstream.UpstreamStatus != http.StatusTooManyRequests || upstream.RetryAfter != 90*time.Second {
		t.Fatalf("UpstreamError = %+v, want transport=502 upstream=429 retry=90s", upstream)
	}
}

func TestImage_InvalidStructuredThrottleMetadataFailsClosed(t *testing.T) {
	for _, retrySeconds := range []int64{-1, 86_401} {
		t.Run(fmt.Sprint(retrySeconds), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusBadGateway, map[string]any{
					"error": "legacy-compatible", "upstreamStatus": 999, "retryAfterSeconds": retrySeconds,
				})
			}))
			defer srv.Close()

			_, _, err := newTestClient(t, srv).Image(context.Background(), 7, "/page/1", "")
			var upstream *sourceengine.UpstreamError
			if !errors.As(err, &upstream) {
				t.Fatalf("Image error = %v, want *UpstreamError", err)
			}
			if upstream.UpstreamStatus != 0 || upstream.RetryAfter != 0 {
				t.Fatalf("invalid metadata survived: %+v", upstream)
			}
		})
	}
}

// TestImage_Success proves POST /image sends {sourceId,pageUrl,imageUrl} and
// returns the RAW response bytes + Content-Type header, NOT a JSON decode.
func TestImage_Success(t *testing.T) {
	var captured map[string]any
	want := []byte{0xFF, 0xD8, 0xFF, 0x00, 0x01, 0x02}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/image" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		decodeBody(t, r, &captured)
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(want)
	}))
	defer srv.Close()

	data, contentType, err := newTestClient(t, srv).Image(context.Background(), 7, "/manga/1/ch/1/page/0", "https://x/p0.jpg")
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("Image data = %v, want %v", data, want)
	}
	if contentType != "image/jpeg" {
		t.Errorf("Image contentType = %q, want %q", contentType, "image/jpeg")
	}
	if captured["pageUrl"] != "/manga/1/ch/1/page/0" || captured["imageUrl"] != "https://x/p0.jpg" {
		t.Errorf("request body = %+v", captured)
	}
}

// TestImage_OmitsEmptyImageURL proves that an empty imageURL is OMITTED from
// the request body entirely (not sent as ""), so the engine host treats it as
// null and falls back to its own getImageUrl resolution.
func TestImage_OmitsEmptyImageURL(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeBody(t, r, &captured)
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte{0x89, 0x50})
	}))
	defer srv.Close()

	if _, _, err := newTestClient(t, srv).Image(context.Background(), 7, "/manga/1/ch/1/page/0", ""); err != nil {
		t.Fatalf("Image: %v", err)
	}
	if _, ok := captured["imageUrl"]; ok {
		t.Errorf("imageUrl must be omitted when empty, got %+v", captured)
	}
}

// TestImage_BadRequest proves a 400 from /image maps to *BadRequestError.
func TestImage_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "unknown sourceId 1"})
	}))
	defer srv.Close()

	_, _, err := newTestClient(t, srv).Image(context.Background(), 1, "/p", "")
	assertBadRequestError(t, err)
}

// TestImage_UpstreamFailure proves a 502 from /image maps to *UpstreamError.
func TestImage_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "fetch failed"})
	}))
	defer srv.Close()

	_, _, err := newTestClient(t, srv).Image(context.Background(), 1, "/p", "")
	assertUpstreamError(t, err, http.StatusBadGateway)
	var upstream *sourceengine.UpstreamError
	errors.As(err, &upstream)
	if upstream.UpstreamStatus != 0 || upstream.RetryAfter != 0 {
		t.Fatalf("legacy error gained structured metadata: %+v", upstream)
	}
}

// TestImage_NetworkFailure_IsWrapped proves a transport-level failure on the
// raw-bytes path (doRaw) is wrapped and returned, mirroring the JSON path's
// TestClient_NetworkFailure_IsWrapped.
func TestImage_NetworkFailure_IsWrapped(t *testing.T) {
	c := sourceengine.New("http://engine-host.invalid", failingDoer{}, "test-engine-control-token")
	if _, _, err := c.Image(context.Background(), 1, "/p", ""); err == nil {
		t.Fatal("Image: want error from a failing doer, got nil")
	}
}

func TestFetcher_EmptyPageURLUsesReaderContext(t *testing.T) {
	want := validJPEG(t)
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pages" {
			writeJSON(t, w, http.StatusOK, map[string]any{"pages": []map[string]any{{"index": 0, "url": "", "imageUrl": "https://images.test/reader.jpg"}}})
			return
		}
		var body map[string]any
		decodeBody(t, r, &body)
		if body["reader"] != true || body["pageUrl"] != "" || body["imageUrl"] != "https://images.test/reader.jpg" {
			t.Errorf("reader request did not preserve context/address pair: %+v", body)
		}
		attempts++
		if attempts == 1 {
			writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "HTTP error 503"})
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(want)
	}))
	defer srv.Close()
	got, err := sourceengine.NewFetcher(newTestClient(t, srv), t.TempDir()).Fetch(context.Background(), fetcher.FetchRef{Provider: "7", URL: "/chapter"})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(got.Pages) != 1 || !bytes.Equal(got.Pages[0].Data, want) {
		t.Fatalf("unexpected pages: %+v", got)
	}
}

func TestReaderImage_CancelsHTTP(t *testing.T) {
	entered := make(chan struct{})
	stopped := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(stopped)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := newTestClient(t, srv).ReaderImage(ctx, 7, "", "https://images.test/reader.jpg")
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request not received")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not cancel")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not observe cancellation")
	}
}
