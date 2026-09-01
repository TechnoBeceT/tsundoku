package provideraddress_test

import (
	"context"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/provideraddress"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

func TestPersistResolvedOnlyPromotesUnknown(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	series := client.Series.Create().SetTitle("Mode").SetSlug("mode").SaveX(ctx)
	provider := client.SeriesProvider.Create().SetSeries(series).SetProvider("42").SaveX(ctx)

	if err := provideraddress.PersistResolved(ctx, client, provider.ID, sourceengine.AddressModeDirect); err != nil {
		t.Fatalf("persist direct: %v", err)
	}
	if err := provideraddress.PersistResolved(ctx, client, provider.ID, sourceengine.AddressModeUnknown); err != nil {
		t.Fatalf("persist stale unknown: %v", err)
	}
	if err := provideraddress.PersistResolved(ctx, client, provider.ID, sourceengine.AddressModeURLSearch); err != nil {
		t.Fatalf("persist conflicting known: %v", err)
	}

	got := client.SeriesProvider.GetX(ctx, provider.ID)
	if got.AddressMode != seriesprovider.AddressModeDirect {
		t.Fatalf("address mode = %q, want direct", got.AddressMode)
	}
}

func TestPersistResolvedForAddressRequiresExactTuple(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	series := client.Series.Create().SetTitle("Address Tuple").SetSlug("address-tuple").SaveX(ctx)

	tests := []struct {
		name   string
		url    string
		webURL string
		want   seriesprovider.AddressMode
	}{
		{name: "different url", url: "current-key", webURL: "https://legacy.example/title", want: seriesprovider.AddressModeUnknown},
		{name: "different web url", url: "legacy-key", webURL: "https://current.example/title", want: seriesprovider.AddressModeUnknown},
		{name: "exact pair", url: "legacy-key", webURL: "https://legacy.example/title", want: seriesprovider.AddressModeDirect},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := client.SeriesProvider.Create().
				SetSeries(series).
				SetProvider(tt.name).
				SetURL("legacy-key").
				SetWebURL("https://legacy.example/title").
				SaveX(ctx)

			if err := provideraddress.PersistResolvedForAddress(ctx, client, provider.ID, tt.url, tt.webURL, sourceengine.AddressModeDirect); err != nil {
				t.Fatalf("persist for address: %v", err)
			}
			if got := client.SeriesProvider.GetX(ctx, provider.ID).AddressMode; got != tt.want {
				t.Fatalf("address mode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPersistResolvedConcurrentNeverReturnsToUnknown(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	series := client.Series.Create().SetTitle("Concurrent Mode").SetSlug("concurrent-mode").SaveX(ctx)
	provider := client.SeriesProvider.Create().SetSeries(series).SetProvider("42").SaveX(ctx)

	start := make(chan struct{})
	errCh := make(chan error, 3)
	var wg sync.WaitGroup
	for _, mode := range []sourceengine.AddressMode{
		sourceengine.AddressModeUnknown,
		sourceengine.AddressModeDirect,
		sourceengine.AddressModeURLSearch,
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errCh <- provideraddress.PersistResolved(ctx, client, provider.ID, mode)
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent persist: %v", err)
		}
	}

	got := client.SeriesProvider.GetX(ctx, provider.ID)
	if got.AddressMode == seriesprovider.AddressModeUnknown {
		t.Fatal("concurrent resolved updates left address mode unknown")
	}
}
