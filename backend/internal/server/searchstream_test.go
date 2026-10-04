package server_test

import (
	"bufio"
	"context"
	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/handler/searchstream"
	"github.com/technobecet/tsundoku/internal/imports"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProgressiveSearchSkipsGzipAndFlushes(t *testing.T) {
	e, _ := newTestServer(t)
	for _, path := range []string{"/api/search", "/api/library/imports/match"} {
		e.GET(path, func(c echo.Context) error {
			if c.QueryParam("stream") != "true" {
				return c.JSON(200, strings.Repeat("result", 1000))
			}
			return searchstream.Serve(c, func(ctx context.Context, emit func(imports.SearchSnapshotDTO) error) error {
				if err := emit(imports.SearchSnapshotDTO{Groups: []imports.SearchGroupDTO{}, PendingSources: []imports.SourceDTO{}, Done: false}); err != nil {
					return err
				}
				<-ctx.Done()
				return ctx.Err()
			})
		})
	}
	server := httptest.NewServer(e)
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	for _, path := range []string{"/api/search", "/api/library/imports/match"} {
		verifySearchCompression(t, client, server.URL, path)
	}
}

func verifySearchCompression(t *testing.T, client *http.Client, serverURL, path string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, serverURL+path+"?stream=true", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	r, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if r.Header.Get("Content-Encoding") != "" || r.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream headers=%v", r.Header)
	}
	scanner := bufio.NewScanner(r.Body)
	if !scanner.Scan() || scanner.Text() != "event: search" {
		t.Fatalf("first frame=%q error=%v", scanner.Text(), scanner.Err())
	}
	if err := r.Body.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodGet, serverURL+path, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	r, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if r.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("JSON lost compression: %v", r.Header)
	}
}
