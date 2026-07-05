package download_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// progressEvent is the decoded download.progress payload used for assertions.
type progressEvent struct {
	ChapterID uuid.UUID `json:"chapter_id"`
	State     string    `json:"state"`
	Current   int       `json:"current"`
	Total     int       `json:"total"`
}

// collectEvents drains the SSE channel until either `done` events of the given
// terminal type are seen or the timeout elapses, returning all events received.
func collectEvents(events <-chan sse.Event, terminal string, timeout time.Duration) []sse.Event {
	var got []sse.Event
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return got
			}
			got = append(got, ev)
			if ev.Type == terminal {
				return got
			}
		case <-deadline:
			return got
		}
	}
}

// rawOf returns the raw JSON bytes of an SSE event's pre-marshalled payload.
func rawOf(t *testing.T, ev sse.Event) []byte {
	t.Helper()
	raw, ok := ev.Data.(json.RawMessage)
	if !ok {
		t.Fatalf("event %q Data is %T, want json.RawMessage", ev.Type, ev.Data)
	}
	return raw
}

// TestProgressSink_ThrottleAndFinal exercises the throttle rule directly (no DB):
// two rapid calls within the throttle window emit only the first, and the final
// page (current == total) ALWAYS emits even when it lands inside the window.
func TestProgressSink_ThrottleAndFinal(t *testing.T) {
	t.Parallel()

	hub := sse.NewHub()
	events, unsub := hub.Subscribe()
	defer unsub()

	// The sink only needs the Dispatcher's hub; client/fetcher/settings are unused.
	d := download.New(nil, nil, hub, download.Config{}, nil)
	chapterID := uuid.New()
	sink := d.ProgressSink(chapterID, "downloading")

	// total<=0 is a no-op guard (empty chapter): must emit nothing.
	sink(0, 0)
	// Three rapid calls: first emits, second is throttled, third is the final page.
	sink(1, 3)
	sink(2, 3)
	sink(3, 3)

	got := collectEvents(events, "", 500*time.Millisecond)

	var progress []progressEvent
	for _, ev := range got {
		if ev.Type != "download.progress" {
			t.Errorf("unexpected event type %q", ev.Type)
			continue
		}
		var pe progressEvent
		if err := json.Unmarshal(rawOf(t, ev), &pe); err != nil {
			t.Fatalf("unmarshal progress event: %v", err)
		}
		progress = append(progress, pe)
	}

	// Only the first (1,3) and the final (3,3) survive the throttle.
	if len(progress) != 2 {
		t.Fatalf("progress events: got %d (%+v), want 2", len(progress), progress)
	}
	if progress[0].Current != 1 || progress[0].Total != 3 {
		t.Errorf("first progress: got (%d,%d), want (1,3)", progress[0].Current, progress[0].Total)
	}
	if progress[1].Current != 3 || progress[1].Total != 3 {
		t.Errorf("final progress: got (%d,%d), want (3,3)", progress[1].Current, progress[1].Total)
	}
	for _, pe := range progress {
		if pe.ChapterID != chapterID {
			t.Errorf("progress chapter_id: got %s, want %s", pe.ChapterID, chapterID)
		}
		if pe.State != "downloading" {
			t.Errorf("progress state: got %q, want downloading", pe.State)
		}
	}
}

// progressDrivingFetcher wraps a base fetcher and, on Fetch, drives the
// context-carried progress sink through (1,3),(2,3),(3,3) before delegating to the
// base — simulating the suwayomi fetcher's per-page progress on the real download
// path.
type progressDrivingFetcher struct {
	base fetcher.ChapterFetcher
}

func (p *progressDrivingFetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	sink := fetcher.ProgressFrom(ctx)
	sink(1, 3)
	sink(2, 3)
	sink(3, 3)
	return p.base.Fetch(ctx, ref)
}

// TestDispatcher_DownloadProgress verifies that a real download cycle broadcasts
// download.progress carrying {current,total} (final page always emitted) and that
// the existing download.start / download.done payloads carry NO current/total keys
// (the omitempty guard keeps them byte-identical).
func TestDispatcher_DownloadProgress(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	events, unsub := hub.Subscribe()
	defer unsub()

	s := client.Series.Create().SetTitle("Progress Series").SetSlug("progress-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-prog").
		SetURL("https://mangadex.org/ch-prog").
		SetProviderIndex(0).
		SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-prog").SaveX(ctx)

	f := &progressDrivingFetcher{base: fake.New(fake.WithPages(3))}
	d := download.New(client, f, hub, download.Config{
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := collectEvents(events, "download.done", 3*time.Second)

	var (
		sawFinalProgress bool
		progressCount    int
	)
	for _, ev := range got {
		switch ev.Type {
		case "download.progress":
			progressCount++
			var pe progressEvent
			if err := json.Unmarshal(rawOf(t, ev), &pe); err != nil {
				t.Fatalf("unmarshal progress event: %v", err)
			}
			if pe.Total <= 0 {
				t.Errorf("download.progress must carry Total>0, got %+v", pe)
			}
			if pe.ChapterID != ch.ID {
				t.Errorf("download.progress chapter_id: got %s, want %s", pe.ChapterID, ch.ID)
			}
			if pe.Current == 3 && pe.Total == 3 {
				sawFinalProgress = true
			}
		case "download.start", "download.done":
			// omitempty guard: these payloads must NOT contain the progress keys.
			raw := string(rawOf(t, ev))
			if strings.Contains(raw, "current") || strings.Contains(raw, "total") {
				t.Errorf("%s payload must omit current/total, got %s", ev.Type, raw)
			}
		}
	}

	if progressCount == 0 {
		t.Error("expected at least one download.progress event, got none")
	}
	if !sawFinalProgress {
		t.Error("expected a final download.progress {current:3,total:3} (always-emit rule)")
	}
}
