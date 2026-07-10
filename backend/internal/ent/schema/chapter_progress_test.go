package schema_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent/chapter"
)

// TestChapterProgressDefaults proves the reading-progress fields default to the
// zero-work migration values: a freshly-created chapter is unread, on page 0,
// with no read_at timestamp. This locks in the "existing rows migrate as unread"
// guarantee.
func TestChapterProgressDefaults(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Progress Series").SetSlug("progress-series").SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).SetChapterKey("1").SetState(chapter.StateWanted).SaveX(ctx)

	if ch.Read {
		t.Errorf("read: want false, got true")
	}
	if ch.LastReadPage != 0 {
		t.Errorf("last_read_page: want 0, got %d", ch.LastReadPage)
	}
	if ch.ReadAt != nil {
		t.Errorf("read_at: want nil, got %v", ch.ReadAt)
	}
}
