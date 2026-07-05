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

// collectEvents drains the SSE channel until the given terminal event type is
// seen or the timeout elapses, returning all events received (inclusive).
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

// decodeProgress returns the decoded download.progress events from got (others
// are skipped), so tests assert only over the progress stream.
func decodeProgress(t *testing.T, got []sse.Event) []progressEvent {
	t.Helper()
	var out []progressEvent
	for _, ev := range got {
		if ev.Type != "download.progress" {
			continue
		}
		var pe progressEvent
		if err := json.Unmarshal(rawOf(t, ev), &pe); err != nil {
			t.Fatalf("unmarshal progress event: %v", err)
		}
		out = append(out, pe)
	}
	return out
}

// assertProgress checks a single download.progress event's chapter, page counts,
// and (downloading) state.
func assertProgress(t *testing.T, pe progressEvent, chID uuid.UUID, current, total int) {
	t.Helper()
	if pe.ChapterID != chID {
		t.Errorf("progress chapter_id: got %s, want %s", pe.ChapterID, chID)
	}
	if pe.Current != current || pe.Total != total {
		t.Errorf("progress pages: got (%d,%d), want (%d,%d)", pe.Current, pe.Total, current, total)
	}
	if pe.State != string(entStateDownloading) {
		t.Errorf("progress state: got %q, want %q", pe.State, entStateDownloading)
	}
}

// entStateDownloading is the state string a download-path progress event carries.
const entStateDownloading = "downloading"

// assertNoProgressKeys asserts the start/done payloads omit the current/total
// keys (the omitempty guard keeps them byte-identical to pre-feature payloads).
func assertNoProgressKeys(t *testing.T, got []sse.Event) {
	t.Helper()
	for _, ev := range got {
		if ev.Type != "download.start" && ev.Type != "download.done" {
			continue
		}
		raw := string(rawOf(t, ev))
		if strings.Contains(raw, "current") || strings.Contains(raw, "total") {
			t.Errorf("%s payload must omit current/total, got %s", ev.Type, raw)
		}
	}
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
	sink := d.ProgressSink(chapterID, entStateDownloading)

	sink(0, 0) // total<=0 guard (empty chapter): emits nothing.
	sink(1, 3) // first: emits.
	sink(2, 3) // within the window: throttled.
	sink(3, 3) // final page: always emits.

	progress := decodeProgress(t, collectEvents(events, "", 500*time.Millisecond))
	if len(progress) != 2 {
		t.Fatalf("progress events: got %d (%+v), want 2", len(progress), progress)
	}
	assertProgress(t, progress[0], chapterID, 1, 3)
	assertProgress(t, progress[1], chapterID, 3, 3)
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
// the existing download.start / download.done payloads carry NO current/total keys.
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
	d := download.New(client, f, hub, download.Config{Storage: storageDir}, settings.Static{Retries: 3, Backoff: time.Hour})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := collectEvents(events, "download.done", 3*time.Second)
	assertNoProgressKeys(t, got)

	progress := decodeProgress(t, got)
	if len(progress) == 0 {
		t.Fatal("expected at least one download.progress event, got none")
	}
	sawFinal := false
	for _, pe := range progress {
		if pe.Total <= 0 {
			t.Errorf("download.progress must carry Total>0, got %+v", pe)
		}
		if pe.ChapterID != ch.ID {
			t.Errorf("download.progress chapter_id: got %s, want %s", pe.ChapterID, ch.ID)
		}
		if pe.Current == 3 && pe.Total == 3 {
			sawFinal = true
		}
	}
	if !sawFinal {
		t.Error("expected a final download.progress {current:3,total:3} (always-emit rule)")
	}
}
