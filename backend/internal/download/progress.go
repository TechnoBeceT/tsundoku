package download

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/fetcher"
)

// progressThrottle is the minimum gap between two download.progress broadcasts
// for the same chapter. It smooths a fast page burst into at most ~4 events/sec
// per chapter — the SSE hub drops on a full 16-event buffer, so throttling is a
// CORRECTNESS guard: an un-throttled burst could evict the terminal download.done
// for a slow subscriber. The final page bypasses this (see progressSink).
const progressThrottle = 250 * time.Millisecond

// progressSink builds the per-chapter progress closure the suwayomi fetcher drives
// (one page at a time). It broadcasts a download.progress SSE event carrying the
// running page count, throttled to at most one every progressThrottle — EXCEPT the
// final page (current == total), which ALWAYS emits so the UI lands on 100% and the
// throttle can never swallow the completion tick.
//
// The closure owns its throttle state (last-emit time) behind a mutex: although the
// fetcher drives it sequentially from one chapter's fetch, the sink is invoked from
// the per-chapter fetch goroutine while the cycle's other goroutines run, so the
// guard keeps the last-emit read/write race-free. state is the chapter's current
// state ("downloading" for a fresh download, "upgrading" for an upgrade) so the
// frontend can label the row. A total of 0 (empty chapter) emits nothing — the FE
// then never divides by zero.
func (d *Dispatcher) progressSink(chapterID uuid.UUID, state string) fetcher.ProgressFunc {
	var (
		mu       sync.Mutex
		lastEmit time.Time
	)
	return func(current, total int) {
		if total <= 0 {
			return
		}

		mu.Lock()
		now := time.Now()
		final := current >= total
		emit := final || now.Sub(lastEmit) >= progressThrottle
		if emit {
			lastEmit = now
		}
		mu.Unlock()

		if !emit {
			return
		}
		d.broadcast("download.progress", DownloadEvent{
			ChapterID: chapterID,
			State:     state,
			Current:   current,
			Total:     total,
		})
	}
}
