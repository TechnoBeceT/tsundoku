package series_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
)

// TestCoverFieldDefaults proves that new rows have the correct zero values for
// the M10 cover/metadata-source fields so existing data needs no migration.
func TestCoverFieldDefaults(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	// A Series created without an explicit metadata_provider_id must default to nil.
	s := db.Series.Create().SetTitle("Cover Test Series").SetSlug("cover-test-series").SaveX(ctx)
	if s.MetadataProviderID != nil {
		t.Fatalf("new series MetadataProviderID = %v, want nil (default)", s.MetadataProviderID)
	}

	// A SeriesProvider created without an explicit cover_url must default to "".
	sp := db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("test-provider").
		SaveX(ctx)
	if sp.CoverURL != "" {
		t.Fatalf("new SeriesProvider CoverURL = %q, want \"\" (default)", sp.CoverURL)
	}
}
