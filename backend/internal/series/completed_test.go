package series_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
)

// TestSeriesCompletedDefaultsFalse proves a newly-created series is not
// completed by default, so existing rows backfill safely (zero migration).
func TestSeriesCompletedDefaultsFalse(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Default Test").SetSlug("default-test").SaveX(ctx)

	if s.Completed {
		t.Fatalf("new series Completed = true, want false (default)")
	}
}
