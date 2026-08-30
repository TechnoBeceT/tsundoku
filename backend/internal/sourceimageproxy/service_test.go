package sourceimageproxy_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	settingssvc "github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceimageproxy"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

var errCatalogOffline = errors.New("engine source catalog offline")

type fakeCatalog struct {
	sources []sourceengine.Source
	err     error
}

func (f fakeCatalog) Sources(context.Context) ([]sourceengine.Source, error) {
	return f.sources, f.err
}

type transportDefaults struct{}

func (transportDefaults) ImageConnectionMode(context.Context) sourcetransport.ImageConnectionMode {
	return sourcetransport.ImageConnectionFresh
}

func (transportDefaults) ResolveBypassSession(context.Context, int64, *bool) (bool, sourcetransport.BypassSessionMode, error) {
	return false, sourcetransport.BypassSessionDisabled, nil
}

type transportCatalog struct{}

func (transportCatalog) RequireSource(context.Context, int64) error { return nil }

func newService(t *testing.T, catalog fakeCatalog) (*sourceimageproxy.Service, *ent.Client, *settingssvc.Service) {
	t.Helper()
	client := testdb.New(t)
	settings := settingssvc.NewService(client, settingssvc.Defaults{})
	transport := sourcetransport.NewService(client, transportDefaults{}, transportCatalog{})
	return sourceimageproxy.NewService(client, settings, transport, catalog), client, settings
}

func TestImageProxyUpdateEnablesDisablesAndCanonicalizesMembership(t *testing.T) {
	ctx := context.Background()
	large := int64(1998416842837112832)
	svc, _, _ := newService(t, fakeCatalog{sources: []sourceengine.Source{{ID: large}, {ID: -42}}})

	first, err := svc.Update(ctx, large, true)
	if err != nil {
		t.Fatalf("enable large source: %v", err)
	}
	if !first.Enabled || first.Intent.SourceID != large || first.Intent.DesiredRevision != 1 {
		t.Fatalf("first result = %+v", first)
	}
	assertIDs(t, first.SourceIDs, []int64{large})

	second, err := svc.Update(ctx, -42, true)
	if err != nil {
		t.Fatalf("enable signed source: %v", err)
	}
	assertIDs(t, second.SourceIDs, []int64{-42, large})

	third, err := svc.Update(ctx, large, false)
	if err != nil {
		t.Fatalf("disable large source: %v", err)
	}
	if third.Enabled {
		t.Fatal("disabled result reports enabled")
	}
	assertIDs(t, third.SourceIDs, []int64{-42})
	if third.Intent.DesiredRevision != 2 {
		t.Fatalf("large source desired revision = %d, want 2", third.Intent.DesiredRevision)
	}
}

func TestImageProxyConcurrentDisjointUpdatesDoNotLoseMembership(t *testing.T) {
	ctx := context.Background()
	svc, _, settings := newService(t, fakeCatalog{sources: []sourceengine.Source{{ID: -42}, {ID: 99}}})

	start := make(chan struct{})
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, id := range []int64{-42, 99} {
		go func(sourceID int64) {
			ready.Done()
			<-start
			_, err := svc.Update(ctx, sourceID, true)
			errs <- err
		}(id)
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent update: %v", err)
		}
	}
	assertIDs(t, settings.ImpersonateSources(ctx), []int64{-42, 99})
}

func TestImageProxyUpdateRejectsUnknownSourceBeforePersistence(t *testing.T) {
	ctx := context.Background()
	svc, client, settings := newService(t, fakeCatalog{sources: []sourceengine.Source{{ID: 7}}})

	_, err := svc.Update(ctx, 8, true)
	if !errors.Is(err, sourceimageproxy.ErrSourceNotFound) {
		t.Fatalf("Update error = %v, want ErrSourceNotFound", err)
	}
	if got := settings.ImpersonateSources(ctx); len(got) != 0 {
		t.Fatalf("membership after rejection = %v, want empty", got)
	}
	if got := client.SourceRuntimeIntent.Query().CountX(ctx); got != 0 {
		t.Fatalf("intent rows after rejection = %d, want 0", got)
	}
}

func TestImageProxyUpdateFailsClosedWhenCatalogUnavailable(t *testing.T) {
	ctx := context.Background()
	svc, client, settings := newService(t, fakeCatalog{err: errCatalogOffline})

	_, err := svc.Update(ctx, 8, true)
	if !errors.Is(err, sourceimageproxy.ErrCatalogUnavailable) || !errors.Is(err, errCatalogOffline) {
		t.Fatalf("Update error = %v, want wrapped catalog unavailable", err)
	}
	if got := settings.ImpersonateSources(ctx); len(got) != 0 {
		t.Fatalf("membership after catalog outage = %v, want empty", got)
	}
	if got := client.SourceRuntimeIntent.Query().CountX(ctx); got != 0 {
		t.Fatalf("intent rows after catalog outage = %d, want 0", got)
	}
}

func TestImageProxyUpdateRollsBackMembershipWhenIntentAdvanceFails(t *testing.T) {
	ctx := context.Background()
	svc, client, settings := newService(t, fakeCatalog{sources: []sourceengine.Source{{ID: 7}}})
	client.SourceRuntimeIntent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if m, ok := mutation.(*ent.SourceRuntimeIntentMutation); ok && m.Op().Is(ent.OpCreate) {
				return nil, errors.New("injected source intent failure")
			}
			return next.Mutate(ctx, mutation)
		})
	})

	_, err := svc.Update(ctx, 7, true)
	if err == nil || !strings.Contains(err.Error(), "injected source intent failure") {
		t.Fatalf("Update error = %v, want injected intent failure", err)
	}
	if got := settings.ImpersonateSources(ctx); len(got) != 0 {
		t.Fatalf("membership after rollback = %v, want empty", got)
	}
	if got := client.SourceRuntimeIntent.Query().CountX(ctx); got != 0 {
		t.Fatalf("intent rows after rollback = %d, want 0", got)
	}
}

func assertIDs(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("source ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("source ids = %v, want %v", got, want)
		}
	}
}
