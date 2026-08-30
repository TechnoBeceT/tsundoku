package sourceconfiguration

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

type sourceCatalog interface {
	Sources(context.Context) ([]sourceengine.Source, error)
}

type globalSnapshot struct {
	WarmupInterval        time.Duration
	WarmupSlowThresholdMs int
	FailureThreshold      int
	SourceCooldown        time.Duration
	PolitenessDelay       time.Duration
	BypassEnabled         bool
	BypassURL             string
	BypassSession         string
	ProxyEnabled          bool
	ProxyURL              string
	ProxySourceIDs        []int64
}

type globalSnapshotter interface {
	Snapshot(context.Context) (globalSnapshot, error)
}

type globalSettings interface {
	RuntimeConfigSnapshot(context.Context) (settings.RuntimeConfigSnapshot, error)
	WarmupInterval(context.Context) time.Duration
	WarmupSlowThresholdMs(context.Context) int
	SourcesFailureThreshold(context.Context) int
	SourcesCooldown(context.Context) time.Duration
	SourcesMinRequestDelay(context.Context) time.Duration
}

type settingsSnapshotter struct{ settings globalSettings }

func (s settingsSnapshotter) Snapshot(ctx context.Context) (globalSnapshot, error) {
	runtime, err := s.settings.RuntimeConfigSnapshot(ctx)
	if err != nil {
		return globalSnapshot{}, fmt.Errorf("global runtime settings: %w", err)
	}
	return globalSnapshot{
		WarmupInterval:        s.settings.WarmupInterval(ctx),
		WarmupSlowThresholdMs: s.settings.WarmupSlowThresholdMs(ctx),
		FailureThreshold:      s.settings.SourcesFailureThreshold(ctx),
		SourceCooldown:        s.settings.SourcesCooldown(ctx),
		PolitenessDelay:       s.settings.SourcesMinRequestDelay(ctx),
		BypassEnabled:         runtime.FlareSolverrEnabled,
		BypassURL:             runtime.FlareSolverrURL,
		BypassSession:         runtime.FlareSolverrSessionName,
		ProxyEnabled:          runtime.ImpersonateEnabled,
		ProxyURL:              runtime.ImpersonateURL,
		ProxySourceIDs:        append([]int64(nil), runtime.ImpersonateSources...),
	}, nil
}

type throughputSnapshot struct {
	Defaults  sourcethroughput.Effective
	Overrides map[int64]sourcethroughput.Override
}

type throughputSnapshotter interface {
	Snapshot(context.Context) (throughputSnapshot, error)
}

type throughputStore interface {
	Defaults(context.Context) sourcethroughput.Effective
	Snapshot(context.Context) (map[int64]sourcethroughput.Override, error)
}

type throughputStoreSnapshotter struct{ store throughputStore }

func (s throughputStoreSnapshotter) Snapshot(ctx context.Context) (throughputSnapshot, error) {
	defaults := s.store.Defaults(ctx)
	overrides, err := s.store.Snapshot(ctx)
	if err != nil {
		return throughputSnapshot{}, err
	}
	return throughputSnapshot{Defaults: defaults, Overrides: overrides}, nil
}

type transportSnapshot struct {
	DefaultImageConnectionMode sourcetransport.ImageConnectionMode
	Overrides                  map[int64]sourcetransport.Override
}

type transportSnapshotter interface {
	Snapshot(context.Context) (transportSnapshot, error)
}

type transportStore interface {
	Snapshot(context.Context) (map[int64]sourcetransport.Override, error)
}

type imageConnectionDefaults interface {
	ImageConnectionMode(context.Context) sourcetransport.ImageConnectionMode
}

type transportStoreSnapshotter struct {
	store    transportStore
	defaults imageConnectionDefaults
}

func (s transportStoreSnapshotter) Snapshot(ctx context.Context) (transportSnapshot, error) {
	mode := s.defaults.ImageConnectionMode(ctx)
	overrides, err := s.store.Snapshot(ctx)
	if err != nil {
		return transportSnapshot{}, err
	}
	return transportSnapshot{DefaultImageConnectionMode: mode, Overrides: overrides}, nil
}

type routingSnapshot struct {
	Resolved      map[int64]network.ResolvedBinding
	Stored        map[int64]network.BindingDTO
	EndpointNames map[string]string
}

type routingSnapshotter interface {
	Snapshot(context.Context) (routingSnapshot, error)
}

type routingStore interface {
	RoutingSnapshot(context.Context) ([]network.ResolvedBinding, error)
	ListBindings(context.Context) ([]network.BindingDTO, error)
	ListEndpoints(context.Context) ([]network.EndpointDTO, error)
}

type routingStoreSnapshotter struct{ store routingStore }

func (s routingStoreSnapshotter) Snapshot(ctx context.Context) (routingSnapshot, error) {
	resolved, err := s.store.RoutingSnapshot(ctx)
	if err != nil {
		return routingSnapshot{}, err
	}
	stored, err := s.store.ListBindings(ctx)
	if err != nil {
		return routingSnapshot{}, err
	}
	endpoints, err := s.store.ListEndpoints(ctx)
	if err != nil {
		return routingSnapshot{}, err
	}
	out := routingSnapshot{
		Resolved:      make(map[int64]network.ResolvedBinding, len(resolved)),
		Stored:        make(map[int64]network.BindingDTO, len(stored)),
		EndpointNames: make(map[string]string, len(endpoints)),
	}
	for _, binding := range resolved {
		out.Resolved[binding.SourceID] = binding
	}
	for _, binding := range stored {
		sourceID, err := strconv.ParseInt(binding.SourceID, 10, 64)
		if err != nil {
			return routingSnapshot{}, fmt.Errorf("parse stored source id %q: %w", binding.SourceID, err)
		}
		out.Stored[sourceID] = binding
	}
	for _, endpoint := range endpoints {
		out.EndpointNames[endpoint.ID] = endpoint.Name
	}
	return out, nil
}

type runtimeSnapshotter interface {
	Snapshot(context.Context) (map[int64]sourcetransport.Intent, error)
}

type runtimeStoreSnapshotter struct{ client *ent.Client }

func (s runtimeStoreSnapshotter) Snapshot(ctx context.Context) (map[int64]sourcetransport.Intent, error) {
	rows, err := s.client.SourceRuntimeIntent.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query source runtime intents: %w", err)
	}
	out := make(map[int64]sourcetransport.Intent, len(rows))
	for _, row := range rows {
		out[row.SourceID] = sourcetransport.Intent{
			SourceID:         row.SourceID,
			DesiredRevision:  row.DesiredRevision,
			AppliedRevision:  row.AppliedRevision,
			LastApplyAttempt: row.LastApplyAttempt,
			LastApplyError:   row.LastApplyError,
		}
	}
	return out, nil
}
