package downloads_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
)

type feedPayloadDriver struct {
	dialect.Driver
	feedSQL  string
	feedRows int
}

func (d *feedPayloadDriver) Query(ctx context.Context, query string, args, v any) error {
	err := d.Driver.Query(ctx, query, args, v)
	if err == nil && strings.Contains(query, `FROM "provider_chapters"`) && !strings.Contains(query, `FROM "chapters"`) {
		d.feedSQL = query
		rows := v.(*entsql.Rows)
		rows.ColumnScanner = &countedFeedRows{ColumnScanner: rows.ColumnScanner, driver: d}
	}
	return err
}

type countedFeedRows struct {
	entsql.ColumnScanner
	driver *feedPayloadDriver
}

func (r *countedFeedRows) Next() bool {
	ok := r.ColumnScanner.Next()
	if ok {
		r.driver.feedRows++
	}
	return ok
}

func TestActivityLoadsOnlyRequestedFeedAddressesWithoutPageLinks(t *testing.T) {
	ctx := context.Background()
	_, db := testdb.NewWithSQL(t)
	drv := &feedPayloadDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	client := ent.NewClient(ent.Driver(drv))
	seedActivityFeed(ctx, client)
	svc := downloads.NewService(client)
	result, err := svc.List(ctx, downloads.ListFilter{States: []entchapter.State{entchapter.StateDownloading}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items=%d", len(result.Items))
	}
	assertActivityAttribution(t, result.Items)
	assertNarrowFeed(t, drv, "List")
	drv.feedRows = 0
	counts, err := svc.ActiveSourceCounts(ctx)
	if err != nil || counts["source"] != 2 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
	assertNarrowFeed(t, drv, "ActiveSourceCounts")

}

func seedActivityFeed(ctx context.Context, client *ent.Client) {
	// Each series has the other series' requested key too; filtering on a global
	// key set would load four rows instead of the two requested addresses.
	for i := range 2 {
		row := client.Series.Create().SetTitle(fmt.Sprint(i)).SetSlug(fmt.Sprint(i)).SaveX(ctx)
		sp := client.SeriesProvider.Create().SetSeriesID(row.ID).SetProvider("source").SetImportance(20).SaveX(ctx)
		for j := range 50 {
			key := fmt.Sprintf("chapter-%d", j)
			client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey(key).SetName("name-" + key).SetProviderIndex(j).SetURL("https://example.org/" + key).SetPageLinks([]fetcher.PageLink{{URL: strings.Repeat("x", 4096)}}).SaveX(ctx)
		}
		client.Chapter.Create().SetSeriesID(row.ID).SetChapterKey(fmt.Sprintf("chapter-%d", i)).SetState(entchapter.StateDownloading).SaveX(ctx)
	}
}

func assertActivityAttribution(t *testing.T, items []downloads.DownloadChapterDTO) {
	t.Helper()
	for _, item := range items {
		if !strings.HasPrefix(item.Name, "name-chapter-") || item.Provider != "source" {
			t.Errorf("attribution lost: %+v", item)
		}
	}
}
func assertNarrowFeed(t *testing.T, drv *feedPayloadDriver, label string) {
	t.Helper()
	t.Logf("%s hydrated feed rows=%d page_links_selected=%v", label, drv.feedRows, strings.Contains(drv.feedSQL, `"page_links"`))
	if drv.feedRows != 2 || strings.Contains(drv.feedSQL, `"page_links"`) {
		t.Errorf("%s loaded excess feed payload: rows=%d SQL=%s", label, drv.feedRows, drv.feedSQL)
	}
}
