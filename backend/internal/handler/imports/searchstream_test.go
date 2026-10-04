package imports_test

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	handler "github.com/technobecet/tsundoku/internal/handler/imports"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type streamingEngine struct {
	*fakeEngineClient
	release chan struct{}
}

func (c *streamingEngine) Search(ctx context.Context, id int64, q string, p int) (sourceengine.SearchResult, error) {
	if id == 2 {
		select {
		case <-c.release:
		case <-ctx.Done():
			return sourceengine.SearchResult{}, ctx.Err()
		}
	}
	return c.fakeEngineClient.Search(ctx, id, q, p)
}
func TestSearchStreamHTTPFlushAndCompatibility(t *testing.T) {
	engine := &streamingEngine{fakeEngineClient: &fakeEngineClient{sources: []sourceengine.Source{{ID: 1, Name: "Healthy"}, {ID: 2, Name: "Blocked"}}, searchResults: map[int64]sourceengine.SearchResult{1: {Manga: []sourceengine.MangaEntry{{Title: "Alpha", URL: "/alpha"}}}, 2: {Manga: []sourceengine.MangaEntry{{Title: "Beta", URL: "/beta"}}}}}, release: make(chan struct{})}
	svc := imports.NewService(engine, nil, nil, "", 5*time.Second, nil)
	h := handler.NewHandler(svc, nil, nil, nil)
	e := echo.New()
	as := auth.NewService(testSecret)
	e.GET("/api/search", h.Search, middleware.RequireOwner(as, false))
	token, _ := as.Issue(uuid.New())
	server := httptest.NewServer(e)
	defer server.Close()
	client := server.Client()
	client.Timeout = 2 * time.Second
	request := func(path string, authorized bool) *http.Response {
		return streamRequest(t, client, server.URL, token, path, authorized)
	}

	for _, tc := range []struct {
		path   string
		auth   bool
		status int
	}{{"/api/search?q=a&stream=true", false, 401}, {"/api/search?stream=true", true, 400}, {"/api/search?q=a&stream=invalid", true, 400}} {
		r := request(tc.path, tc.auth)
		closeStreamBody(t, r.Body)
		if r.StatusCode != tc.status {
			t.Fatalf("%s status=%d", tc.path, r.StatusCode)
		}
	}
	verifySearchResponses(t, request, engine.release)
}

func streamRequest(t *testing.T, client *http.Client, serverURL, token, path string, authorized bool) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, serverURL+path, nil)
	if authorized {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func nextSearchFrame(t *testing.T, scanner *bufio.Scanner) imports.SearchSnapshotDTO {
	t.Helper()
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			var s imports.SearchSnapshotDTO
			if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &s); err != nil {
				t.Fatal(err)
			}
			return s
		}
	}
	t.Fatalf("stream ended: %v", scanner.Err())
	return imports.SearchSnapshotDTO{}
}

func closeStreamBody(t *testing.T, body io.Closer) {
	t.Helper()
	if err := body.Close(); err != nil {
		t.Error(err)
	}
}

func verifySearchResponses(t *testing.T, request func(string, bool) *http.Response, release chan struct{}) {
	t.Helper()
	resp := request("/api/search?q=alpha&stream=true", true)
	defer closeStreamBody(t, resp.Body)
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type=%s", resp.Header.Get("Content-Type"))
	}
	scanner := bufio.NewScanner(resp.Body)
	next := func() imports.SearchSnapshotDTO { return nextSearchFrame(t, scanner) }
	first := next()
	if first.Done || len(first.PendingSources) != 2 {
		t.Fatalf("initial=%+v", first)
	}
	partial := next()
	if partial.Done || len(partial.Groups) != 1 || len(partial.PendingSources) != 1 {
		t.Fatalf("partial=%+v", partial)
	}
	close(release)
	var final imports.SearchSnapshotDTO
	for !final.Done {
		final = next()
	}
	if len(final.Groups) != 2 || len(final.PendingSources) != 0 {
		t.Fatalf("final=%+v", final)
	}
	verifySearchJSON(t, request)
}

func verifySearchJSON(t *testing.T, request func(string, bool) *http.Response) {
	t.Helper()
	r := request("/api/search?q=alpha", true)
	defer closeStreamBody(t, r.Body)
	var groups []imports.SearchGroupDTO
	if err := json.NewDecoder(r.Body).Decode(&groups); err != nil || len(groups) != 2 {
		t.Fatalf("JSON compatibility groups=%+v err=%v", groups, err)
	}
}
