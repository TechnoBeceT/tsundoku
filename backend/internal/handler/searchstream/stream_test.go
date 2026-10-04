package searchstream_test

import (
	"context"
	"errors"
	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/handler/searchstream"
	"github.com/technobecet/tsundoku/internal/imports"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBackpressureCancelsSearchWithinWriteDeadline(t *testing.T) {
	e := echo.New()
	finished := make(chan bool, 1)
	e.GET("/", func(c echo.Context) error {
		return searchstream.Serve(c, func(ctx context.Context, emit func(imports.SearchSnapshotDTO) error) error {
			err := emit(imports.SearchSnapshotDTO{Groups: []imports.SearchGroupDTO{{Title: strings.Repeat("x", 16<<20)}}, PendingSources: []imports.SourceDTO{}})
			finished <- err != nil && errors.Is(ctx.Err(), context.Canceled)
			return err
		})
	})
	server := httptest.NewServer(e)
	defer server.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case cancelled := <-finished:
		if !cancelled {
			t.Fatal("blocked write did not cancel owned work")
		}
	case <-time.After(7 * time.Second):
		t.Fatal("blocked consumer held search past write deadline")
	}
}
func TestCommittedSearchErrorIsSafeTerminalFrame(t *testing.T) {
	e := echo.New()
	e.GET("/", func(c echo.Context) error {
		return searchstream.Serve(c, func(ctx context.Context, emit func(imports.SearchSnapshotDTO) error) error {
			if err := emit(imports.SearchSnapshotDTO{Groups: []imports.SearchGroupDTO{}, PendingSources: []imports.SourceDTO{}}); err != nil {
				return err
			}
			return errors.New("private upstream details")
		})
	})
	server := httptest.NewServer(e)
	defer server.Close()
	r, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "event: error\ndata: {\"message\":\"Search could not complete\"}") || strings.Contains(body, "private") {
		t.Fatalf("unsafe terminal body: %s", body)
	}
}
