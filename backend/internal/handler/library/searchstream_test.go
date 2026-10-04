package library_test

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	handler "github.com/technobecet/tsundoku/internal/handler/library"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type matchStreamEngine struct {
	*fake.Client
	release chan struct{}
	queries chan string
}

func (c *matchStreamEngine) Search(ctx context.Context, id int64, q string, p int) (sourceengine.SearchResult, error) {
	c.queries <- q
	if id == 2 {
		select {
		case <-c.release:
		case <-ctx.Done():
			return sourceengine.SearchResult{}, ctx.Err()
		}
	}
	return sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{Title: q, URL: "/candidate"}}}, nil
}
func TestMatchStreamHTTPStagedTitleAndErrors(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	_, err := db.ImportEntry.Create().SetPath("/staged/series").SetTitle("Server title").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine := &matchStreamEngine{Client: fake.New(fake.WithSources([]sourceengine.Source{{ID: 1, Name: "Healthy"}, {ID: 2, Name: "Blocked"}})), release: make(chan struct{}), queries: make(chan string, 2)}
	svc := library.NewService(db, nil, imports.NewService(engine, nil, db, "", 5*time.Second, nil), nil, nil, "", nil)
	h := handler.NewHandler(svc)
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	as := auth.NewService(testSecret)
	e.GET("/api/library/imports/match", h.Match, middleware.RequireOwner(as, false))
	token, _ := as.Issue(uuid.New())
	server := httptest.NewServer(e)
	defer server.Close()
	client := server.Client()
	client.Timeout = 2 * time.Second
	request := func(path string, authorized bool) *http.Response {
		return streamRequest(t, client, server.URL, token, path, authorized)
	}

	for _, tc := range []struct {
		query  string
		auth   bool
		status int
	}{{"?path=/staged/series&stream=true", false, 401}, {"?stream=true", true, 400}, {"?path=/missing&stream=true", true, 404}} {
		r := request("/api/library/imports/match"+tc.query, tc.auth)
		closeStreamBody(t, r.Body)
		if r.StatusCode != tc.status {
			t.Fatalf("%s status=%d", tc.query, r.StatusCode)
		}
	}
	verifyStagedMatch(t, request, engine)
}

func streamRequest(t *testing.T, client *http.Client, serverURL, token, path string, authorized bool) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, serverURL+path, nil)
	if authorized {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return r
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

func verifyStagedMatch(t *testing.T, request func(string, bool) *http.Response, engine *matchStreamEngine) {
	t.Helper()
	r := request("/api/library/imports/match?stream=true&path="+url.QueryEscape("/staged/series")+"&q=ClientTitle", true)
	defer closeStreamBody(t, r.Body)
	scanner := bufio.NewScanner(r.Body)
	next := func() imports.SearchSnapshotDTO { return nextSearchFrame(t, scanner) }
	if first := next(); first.Done || len(first.PendingSources) != 2 {
		t.Fatalf("initial=%+v", first)
	}
	if partial := next(); partial.Done || len(partial.Groups) != 1 || partial.Groups[0].Title != "Server title" {
		t.Fatalf("partial=%+v", partial)
	}
	close(engine.release)
	var final imports.SearchSnapshotDTO
	for !final.Done {
		final = next()
	}
	if len(final.Groups) != 1 || len(final.Groups[0].Candidates) != 2 {
		t.Fatalf("final=%+v", final)
	}
	verifyStagedQueries(t, engine.queries)
}

func verifyStagedQueries(t *testing.T, queries <-chan string) {
	t.Helper()
	for i := 0; i < 2; i++ {
		if q := <-queries; q != "Server title" {
			t.Fatalf("query=%q", q)
		}
	}
}
