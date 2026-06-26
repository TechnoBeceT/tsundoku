package series_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/series"
)

// TestStaleGraceResolvedAtUseTime proves NewServiceWithStaleGrace reads the
// stale-grace resolver on every health read (hot reload), not once at
// construction: each LibraryHealth + UnhealthyCount call invokes the resolver.
// If the value were captured at construction the counter would stay at 1.
func TestStaleGraceResolvedAtUseTime(t *testing.T) {
	db := testdb.New(t)
	var calls int64
	svc := series.NewServiceWithStaleGrace(db, t.TempDir(), func(context.Context) int {
		atomic.AddInt64(&calls, 1)
		return 14
	})
	ctx := context.Background()

	if _, err := svc.LibraryHealth(ctx); err != nil {
		t.Fatalf("LibraryHealth: %v", err)
	}
	if _, err := svc.UnhealthyCount(ctx); err != nil {
		t.Fatalf("UnhealthyCount: %v", err)
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Errorf("stale-grace resolver calls = %d, want 2 (read at use-time, not captured)", got)
	}
}
