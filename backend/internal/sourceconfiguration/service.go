package sourceconfiguration

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

var (
	ErrSourceNotFound     = errors.New("source not found")
	ErrCatalogUnavailable = errors.New("source catalog unavailable")
)

type dependencies struct {
	catalog            sourceCatalog
	globals            globalSnapshotter
	throughput         throughputSnapshotter
	transport          transportSnapshotter
	routing            routingSnapshotter
	runtime            runtimeSnapshotter
	defaultKCEFEnabled *bool
}

// Service composes bounded store snapshots into source configuration reads.
type Service struct{ deps dependencies }

// NewService constructs the production composer from the owning domain stores.
func NewService(
	client *ent.Client,
	catalog sourceCatalog,
	globals globalSettings,
	throughput throughputStore,
	transport transportStore,
	routing routingStore,
	transportDefaults imageConnectionDefaults,
	defaultKCEFEnabled bool,
) *Service {
	return newService(dependencies{
		catalog:            catalog,
		globals:            settingsSnapshotter{settings: globals},
		throughput:         throughputStoreSnapshotter{store: throughput},
		transport:          transportStoreSnapshotter{store: transport, defaults: transportDefaults},
		routing:            routingStoreSnapshotter{store: routing},
		runtime:            runtimeStoreSnapshotter{client: client},
		defaultKCEFEnabled: &defaultKCEFEnabled,
	})
}

func newService(deps dependencies) *Service { return &Service{deps: deps} }

// Get returns one live source's stored policy intent and effective configuration.
func (s *Service) Get(ctx context.Context, sourceID int64) (Configuration, error) {
	sources, err := s.loadCatalog(ctx)
	if err != nil {
		return Configuration{}, err
	}
	var source sourceengine.Source
	found := false
	for _, candidate := range sources {
		if candidate.ID == sourceID {
			source, found = candidate, true
			break
		}
	}
	if !found {
		return Configuration{}, fmt.Errorf("sourceconfiguration.Get source %d: %w", sourceID, ErrSourceNotFound)
	}
	snapshot, err := s.loadStores(ctx)
	if err != nil {
		return Configuration{}, fmt.Errorf("sourceconfiguration.Get source %d: %w", sourceID, err)
	}
	profiles := deriveProfileKeys(sources, snapshot.routing, snapshot.transport.Overrides, s.defaultKCEFEnabled())
	return compose(source, snapshot, profiles[sourceID]), nil
}

func (s *Service) defaultKCEFEnabled() bool {
	if s.deps.defaultKCEFEnabled == nil {
		return true
	}
	return *s.deps.defaultKCEFEnabled
}

// Exceptions returns only live sources with at least one field-level exception.
func (s *Service) Exceptions(ctx context.Context) ([]Summary, error) {
	sources, err := s.loadCatalog(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.loadStores(ctx)
	if err != nil {
		return nil, fmt.Errorf("sourceconfiguration.Exceptions: %w", err)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	out := make([]Summary, 0)
	for _, source := range sources {
		count := exceptionCount(source.ID, snapshot)
		if count == 0 {
			continue
		}
		out = append(out, Summary{
			Source:         identity(source),
			ExceptionCount: count,
			Runtime:        runtimeStatus(snapshot.runtime[source.ID]),
		})
	}
	return out, nil
}

func (s *Service) loadCatalog(ctx context.Context) ([]sourceengine.Source, error) {
	sources, err := s.deps.catalog.Sources(ctx)
	if err != nil {
		return nil, fmt.Errorf("sourceconfiguration: %w: %w", ErrCatalogUnavailable, err)
	}
	return sources, nil
}

type storesSnapshot struct {
	globals    globalSnapshot
	throughput throughputSnapshot
	transport  transportSnapshot
	routing    routingSnapshot
	runtime    map[int64]sourcetransport.Intent
}

func (s *Service) loadStores(ctx context.Context) (storesSnapshot, error) {
	globals, err := s.deps.globals.Snapshot(ctx)
	if err != nil {
		return storesSnapshot{}, fmt.Errorf("global settings snapshot: %w", err)
	}
	throughput, err := s.deps.throughput.Snapshot(ctx)
	if err != nil {
		return storesSnapshot{}, fmt.Errorf("throughput snapshot: %w", err)
	}
	transport, err := s.deps.transport.Snapshot(ctx)
	if err != nil {
		return storesSnapshot{}, fmt.Errorf("transport snapshot: %w", err)
	}
	routing, err := s.deps.routing.Snapshot(ctx)
	if err != nil {
		return storesSnapshot{}, fmt.Errorf("routing snapshot: %w", err)
	}
	runtime, err := s.deps.runtime.Snapshot(ctx)
	if err != nil {
		return storesSnapshot{}, fmt.Errorf("runtime snapshot: %w", err)
	}
	return storesSnapshot{globals: globals, throughput: throughput, transport: transport, routing: routing, runtime: runtime}, nil
}
