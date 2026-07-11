package series_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/series"
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

// seedMeta seeds a series with 2 providers (importance 20 + 10) with distinct
// titles and cover_urls. Returns (seriesID, highImportanceProviderID,
// lowImportanceProviderID). Both providers supply a non-empty cover_url so the
// resolution tests can verify the proxy path is conditionally populated.
func seedMeta(ctx context.Context, t *testing.T, client *ent.Client) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	s := client.Series.Create().
		SetTitle("Canonical Title").
		SetSlug("canonical-title").
		SaveX(ctx)
	high := client.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("high-src").
		SetTitle("High Source Title").
		SetCoverURL("/img/high.jpg").
		SetImportance(20).
		SaveX(ctx)
	low := client.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("low-src").
		SetTitle("Low Source Title").
		SetCoverURL("/img/low.jpg").
		SetImportance(10).
		SaveX(ctx)
	return s.ID, high.ID, low.ID
}

// TestMetadataResolution_DefaultHighestImportance proves GetSeries auto-selects the
// highest-importance provider's title/cover when no metadata_provider_id pin is set.
func TestMetadataResolution_DefaultHighestImportance(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seriesID, highID, _ := seedMeta(ctx, t, db)

	svc := series.NewService(db, t.TempDir(), 14)
	got, err := svc.GetSeries(ctx, seriesID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	if got.DisplayName != "High Source Title" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "High Source Title")
	}
	wantCover := "/api/series/" + seriesID.String() + "/cover?v="
	if !strings.HasPrefix(got.CoverURL, wantCover) {
		t.Errorf("CoverURL = %q, want prefix %q", got.CoverURL, wantCover)
	}

	// The highest-importance provider must carry isMetadataSource=true.
	var foundHigh bool
	for _, p := range got.Providers {
		if p.ID == highID.String() {
			foundHigh = true
			if !p.IsMetadataSource {
				t.Errorf("high-importance provider IsMetadataSource = false, want true")
			}
		} else if p.IsMetadataSource {
			t.Errorf("low-importance provider IsMetadataSource = true, want false")
		}
	}
	if !foundHigh {
		t.Fatal("high-importance provider not found in Providers slice")
	}
}

// TestSetMetadataSource_SwitchesDisplayName proves that pinning the low provider
// changes DisplayName and isMetadataSource on the next GetSeries.
func TestSetMetadataSource_SwitchesDisplayName(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seriesID, _, lowID := seedMeta(ctx, t, db)

	svc := series.NewService(db, t.TempDir(), 14)
	if err := svc.SetMetadataSource(ctx, seriesID, &lowID); err != nil {
		t.Fatalf("SetMetadataSource: %v", err)
	}

	got, err := svc.GetSeries(ctx, seriesID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.DisplayName != "Low Source Title" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Low Source Title")
	}

	// The pinned low-importance provider must now be isMetadataSource.
	for _, p := range got.Providers {
		if p.ID == lowID.String() {
			if !p.IsMetadataSource {
				t.Errorf("pinned provider IsMetadataSource = false, want true")
			}
		} else {
			if p.IsMetadataSource {
				t.Errorf("non-pinned provider IsMetadataSource = true, want false")
			}
		}
	}
}

// TestSetMetadataSource_NilResetsToAuto proves that clearing the pin (nil) reverts
// to the highest-importance provider's title.
func TestSetMetadataSource_NilResetsToAuto(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seriesID, _, lowID := seedMeta(ctx, t, db)

	svc := series.NewService(db, t.TempDir(), 14)
	if err := svc.SetMetadataSource(ctx, seriesID, &lowID); err != nil {
		t.Fatalf("SetMetadataSource (pin): %v", err)
	}
	if err := svc.SetMetadataSource(ctx, seriesID, nil); err != nil {
		t.Fatalf("SetMetadataSource (nil reset): %v", err)
	}

	got, err := svc.GetSeries(ctx, seriesID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.DisplayName != "High Source Title" {
		t.Errorf("DisplayName = %q after nil reset, want %q (highest-importance fallback)", got.DisplayName, "High Source Title")
	}
}

// TestSetMetadataSource_ForeignProvider proves that a providerID not belonging to
// the series returns ErrProviderNotInSeries.
func TestSetMetadataSource_ForeignProvider(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seriesID, _, _ := seedMeta(ctx, t, db)

	// A provider that belongs to a different series.
	other := db.Series.Create().SetTitle("Other").SetSlug("other").SaveX(ctx)
	foreignProv := db.SeriesProvider.Create().
		SetSeriesID(other.ID).SetProvider("foreign-src").SaveX(ctx)

	svc := series.NewService(db, t.TempDir(), 14)
	err := svc.SetMetadataSource(ctx, seriesID, &foreignProv.ID)
	if !errors.Is(err, series.ErrProviderNotInSeries) {
		t.Fatalf("SetMetadataSource (foreign) = %v, want ErrProviderNotInSeries", err)
	}
}

// TestSetMetadataSource_NotFound proves that an unknown series id returns ErrSeriesNotFound.
func TestSetMetadataSource_NotFound(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)

	unknown := uuid.New()
	if err := svc.SetMetadataSource(ctx, unknown, nil); !errors.Is(err, series.ErrSeriesNotFound) {
		t.Fatalf("SetMetadataSource (unknown) = %v, want ErrSeriesNotFound", err)
	}
}

// TestRemoveProvider_ClearsDanglingMetadataSource proves that RemoveProvider clears
// the series' metadata_provider_id when it points to the removed provider, so the
// pointer never dangles after the row is gone.
func TestRemoveProvider_ClearsDanglingMetadataSource(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seriesID, highID, _ := seedMeta(ctx, t, db)

	// Pin the high-importance provider as metadata source.
	svc := series.NewService(db, t.TempDir(), 14)
	if err := svc.SetMetadataSource(ctx, seriesID, &highID); err != nil {
		t.Fatalf("SetMetadataSource: %v", err)
	}

	// Remove the pinned provider.
	if err := svc.RemoveProvider(ctx, seriesID, highID); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}

	// The series' metadata_provider_id must now be nil (cleared by the guard).
	row := db.Series.GetX(ctx, seriesID)
	if row.MetadataProviderID != nil {
		t.Errorf("Series.MetadataProviderID = %v after RemoveProvider, want nil", row.MetadataProviderID)
	}
}

// TestProviderDTO_CoverURLPath proves that each ProviderDTO.CoverURL carries the
// expected provider-level proxy path.
func TestProviderDTO_CoverURLPath(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seriesID, highID, _ := seedMeta(ctx, t, db)

	svc := series.NewService(db, t.TempDir(), 14)
	got, err := svc.GetSeries(ctx, seriesID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	for _, p := range got.Providers {
		wantCover := "/api/series/" + seriesID.String() + "/providers/" + p.ID + "/cover"
		if p.CoverURL != wantCover {
			t.Errorf("provider %s CoverURL = %q, want %q", p.ID, p.CoverURL, wantCover)
		}
	}

	// The high-importance provider's Title must be set in the DTO.
	for _, p := range got.Providers {
		if p.ID == highID.String() && p.Title != "High Source Title" {
			t.Errorf("high provider Title = %q, want %q", p.Title, "High Source Title")
		}
	}
}

// TestProviderDTO_CoverURL_Conditional proves that newProviderDTO conditionally
// populates CoverURL: a provider with a non-empty cover_url gets the proxy path;
// a provider with an empty cover_url gets "" so the SPA never fires a cover fetch
// that would 404. This is the non-vacuous RED→GREEN test for the Fix 1 behaviour
// change (old code always emitted the proxy path regardless of cover_url).
func TestProviderDTO_CoverURL_Conditional(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Cover Cond Test").SetSlug("cover-cond-test").SaveX(ctx)
	withCover := db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("src-with-cover").
		SetCoverURL("/img/has-cover.jpg").
		SetImportance(10).
		SaveX(ctx)
	noCover := db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("src-no-cover").
		// Deliberately omit SetCoverURL — defaults to "".
		SetImportance(5).
		SaveX(ctx)

	svc := series.NewService(db, t.TempDir(), 14)
	got, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	byID := make(map[string]series.ProviderDTO, len(got.Providers))
	for _, p := range got.Providers {
		byID[p.ID] = p
	}

	// Provider WITH cover_url → CoverURL must be the proxy path.
	wantPath := "/api/series/" + s.ID.String() + "/providers/" + withCover.ID.String() + "/cover"
	if got := byID[withCover.ID.String()].CoverURL; got != wantPath {
		t.Errorf("provider with cover_url: CoverURL = %q, want %q", got, wantPath)
	}

	// Provider WITHOUT cover_url → CoverURL must be "".
	if got := byID[noCover.ID.String()].CoverURL; got != "" {
		t.Errorf("provider with empty cover_url: CoverURL = %q, want \"\"", got)
	}
}

// TestListSeries_DisplayName proves that ListSeries populates DisplayName from
// the highest-importance provider's title (no-N+1 shape must be preserved).
func TestListSeries_DisplayName(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seriesID, _, _ := seedMeta(ctx, t, db)

	svc := series.NewService(db, t.TempDir(), 14)
	rows, err := svc.ListSeries(ctx, series.ListFilter{})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListSeries: want 1 series, got %d", len(rows))
	}
	if rows[0].DisplayName != "High Source Title" {
		t.Errorf("ListSeries DisplayName = %q, want %q", rows[0].DisplayName, "High Source Title")
	}
	wantCover := "/api/series/" + seriesID.String() + "/cover?v="
	if !strings.HasPrefix(rows[0].CoverURL, wantCover) {
		t.Errorf("ListSeries CoverURL = %q, want prefix %q", rows[0].CoverURL, wantCover)
	}
}

// TestCoverURL_VersionTracksMetadataSource proves the cover URL is CONTENT-VERSIONED:
// the ?v= param is stable while the metadata source's cover_url is unchanged, and
// changes the moment the owner pins a different source. That equivalence is what
// licenses the immutable Cache-Control on the cover endpoint — the URL changes
// exactly when the image does, so the browser can cache it forever and still see a
// metadata-source switch instantly.
//
// It also proves building the DTO does ZERO disk I/O: the service is pointed at a
// storage root that does not exist, and must neither fail nor create anything.
func TestCoverURL_VersionTracksMetadataSource(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seriesID, _, lowID := seedMeta(ctx, t, db)

	storage := filepath.Join(t.TempDir(), "does-not-exist")
	svc := series.NewService(db, storage, 14)

	first, err := svc.GetSeries(ctx, seriesID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	again, err := svc.GetSeries(ctx, seriesID)
	if err != nil {
		t.Fatalf("GetSeries (repeat): %v", err)
	}
	if first.CoverURL != again.CoverURL {
		t.Errorf("cover version is not stable: %q then %q", first.CoverURL, again.CoverURL)
	}

	// Pin the OTHER source (a different cover_url) ⇒ a different image ⇒ a
	// different URL, or the browser would keep showing the old cover forever.
	if err := svc.SetMetadataSource(ctx, seriesID, &lowID); err != nil {
		t.Fatalf("SetMetadataSource: %v", err)
	}
	switched, err := svc.GetSeries(ctx, seriesID)
	if err != nil {
		t.Fatalf("GetSeries (after switch): %v", err)
	}
	if switched.CoverURL == first.CoverURL {
		t.Errorf("cover version unchanged after a metadata-source switch: %q", switched.CoverURL)
	}
	if !strings.HasPrefix(switched.CoverURL, "/api/series/"+seriesID.String()+"/cover?v=") {
		t.Errorf("cover URL = %q, want the versioned proxy path", switched.CoverURL)
	}

	if _, err := os.Stat(storage); !os.IsNotExist(err) {
		t.Errorf("building the DTO touched disk: %q exists (stat err %v)", storage, err)
	}
}
