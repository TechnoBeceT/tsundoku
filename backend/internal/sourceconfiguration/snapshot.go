package sourceconfiguration

import (
	"context"
	"fmt"
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
	DownloadConcurrency   int
	ImageRequestDelay     time.Duration
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
	SourceConfigurationSnapshot(context.Context) (settings.SourceConfigurationSnapshot, error)
}

type settingsSnapshotter struct{ settings globalSettings }

func (s settingsSnapshotter) Snapshot(ctx context.Context) (globalSnapshot, error) {
	snapshot, err := s.settings.SourceConfigurationSnapshot(ctx)
	if err != nil {
		return globalSnapshot{}, fmt.Errorf("source configuration settings: %w", err)
	}
	runtime := snapshot.Runtime
	return globalSnapshot{
		DownloadConcurrency:   snapshot.DownloadConcurrency,
		ImageRequestDelay:     snapshot.ImageRequestDelay,
		WarmupInterval:        snapshot.WarmupInterval,
		WarmupSlowThresholdMs: snapshot.WarmupSlowThresholdMs,
		FailureThreshold:      snapshot.FailureThreshold,
		SourceCooldown:        snapshot.SourceCooldown,
		PolitenessDelay:       snapshot.PolitenessDelay,
		BypassEnabled:         runtime.FlareSolverrEnabled,
		BypassURL:             runtime.FlareSolverrURL,
		BypassSession:         runtime.FlareSolverrSessionName,
		ProxyEnabled:          runtime.ImpersonateEnabled,
		ProxyURL:              runtime.ImpersonateURL,
		ProxySourceIDs:        append([]int64(nil), runtime.ImpersonateSources...),
	}, nil
}

type throughputSnapshot struct {
	Overrides map[int64]sourcethroughput.Override
}

type throughputSnapshotter interface {
	Snapshot(context.Context) (throughputSnapshot, error)
}

type throughputStore interface {
	Snapshot(context.Context) (map[int64]sourcethroughput.Override, error)
}

type throughputStoreSnapshotter struct{ store throughputStore }

func (s throughputStoreSnapshotter) Snapshot(ctx context.Context) (throughputSnapshot, error) {
	overrides, err := s.store.Snapshot(ctx)
	if err != nil {
		return throughputSnapshot{}, err
	}
	return throughputSnapshot{Overrides: overrides}, nil
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
	Stored        map[int64]network.ConfigurationBinding
	EndpointNames map[string]string
}

type routingSnapshotter interface {
	Snapshot(context.Context) (routingSnapshot, error)
}

type routingStore interface {
	ConfigurationSnapshot(context.Context) (network.ConfigurationSnapshot, error)
}

type routingStoreSnapshotter struct{ store routingStore }

func (s routingStoreSnapshotter) Snapshot(ctx context.Context) (routingSnapshot, error) {
	snapshot, err := s.store.ConfigurationSnapshot(ctx)
	if err != nil {
		return routingSnapshot{}, err
	}
	out := routingSnapshot{
		Resolved:      make(map[int64]network.ResolvedBinding, len(snapshot.Resolved)),
		Stored:        make(map[int64]network.ConfigurationBinding, len(snapshot.Stored)),
		EndpointNames: make(map[string]string, len(snapshot.EndpointNames)),
	}
	for _, binding := range snapshot.Resolved {
		out.Resolved[binding.SourceID] = binding
	}
	for _, binding := range snapshot.Stored {
		out.Stored[binding.SourceID] = binding
	}
	for endpointID, name := range snapshot.EndpointNames {
		out.EndpointNames[endpointID] = name
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
