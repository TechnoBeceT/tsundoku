// Package enginetopo_test exercises the enginetopo one-shot maintenance passes
// (extension/repo/apk-cache seeding, source-preference seeding, the DB->engine
// reconcile core) against an ephemeral Postgres (testdb) and the shared
// sourceengine/fake.Client — no JVM, no network. seedProvider is shared by
// every *_test.go in the package (§2 DRY).
package enginetopo_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
)

// seedProvider creates a Series + one SeriesProvider row (url="" unless
// overridden) with the given suwayomi_id, mirroring a real ingested row.
// monitored/completed are irrelevant to every pass in this package, so the
// seed does not bother setting them.
func seedProvider(ctx context.Context, t *testing.T, client *ent.Client, title, provider string, suwayomiID int) *ent.SeriesProvider {
	t.Helper()
	s := client.Series.Create().
		SetTitle(title).
		SetSlug(disk.Slugify(title)).
		SaveX(ctx)
	return client.SeriesProvider.Create().
		SetSeries(s).
		SetProvider(provider).
		SetSuwayomiID(suwayomiID).
		SaveX(ctx)
}
