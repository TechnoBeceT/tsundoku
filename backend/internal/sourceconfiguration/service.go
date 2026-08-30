package sourceconfiguration

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

var (
	ErrSourceNotFound     = errors.New("source not found")
	ErrCatalogUnavailable = errors.New("source catalog unavailable")
)

type dependencies struct {
	catalog    sourceCatalog
	globals    globalSnapshotter
	throughput throughputSnapshotter
	transport  transportSnapshotter
	routing    routingSnapshotter
	runtime    runtimeSnapshotter
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
) *Service {
	return newService(dependencies{
		catalog:    catalog,
		globals:    settingsSnapshotter{settings: globals},
		throughput: throughputStoreSnapshotter{store: throughput},
		transport:  transportStoreSnapshotter{store: transport, defaults: transportDefaults},
		routing:    routingStoreSnapshotter{store: routing},
		runtime:    runtimeStoreSnapshotter{client: client},
	})
}

func newService(deps dependencies) *Service { return &Service{deps: deps} }

// Get returns one live source's complete effective configuration.
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
	profiles := deriveProfileKeys(sources, snapshot.routing, snapshot.transport.Overrides)
	return compose(source, snapshot, profiles[sourceID]), nil
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

func compose(source sourceengine.Source, snapshot storesSnapshot, profileKey string) Configuration {
	sourceID := source.ID
	throughputOverride := snapshot.throughput.Overrides[sourceID]
	throughputEffective := sourcethroughput.ApplyDefaults(snapshot.throughput.Defaults, throughputOverride)
	transportOverride := snapshot.transport.Overrides[sourceID]
	imageMode := snapshot.transport.DefaultImageConnectionMode
	if transportOverride.ImageConnectionMode != nil {
		imageMode = *transportOverride.ImageConnectionMode
	}
	binding := effectiveBinding(sourceID, snapshot.routing)
	reuse, sessionMode := resolveBypassSession(snapshot.globals.BypassSession, binding, transportOverride.ReuseBypassSession)
	optedIn := containsSourceID(snapshot.globals.ProxySourceIDs, sourceID)
	proxyConfigured := snapshot.globals.ProxyURL != ""
	routing := routingConfiguration(binding, snapshot.routing.EndpointNames)
	return Configuration{
		Source: identity(source),
		DownloadConcurrency: IntegerPolicyValue{
			Override: throughputOverride.DownloadConcurrency, Effective: throughputEffective.DownloadConcurrency,
			Inherited: throughputOverride.DownloadConcurrency == nil,
		},
		ImageRequestDelay: DurationPolicyValue{
			Override: throughputOverride.ImageRequestDelay, Effective: throughputEffective.ImageRequestDelay,
			Inherited: throughputOverride.ImageRequestDelay == nil,
		},
		Protection: ProtectionConfiguration{
			WarmupInterval: snapshot.globals.WarmupInterval, WarmupSlowThresholdMs: snapshot.globals.WarmupSlowThresholdMs,
			FailureThreshold: snapshot.globals.FailureThreshold, SourceCooldown: snapshot.globals.SourceCooldown,
			PolitenessDelay: snapshot.globals.PolitenessDelay,
		},
		BypassEnabled: bypassEnabled(binding, snapshot.globals),
		ReuseBypassSession: BypassSessionPolicyValue{
			Override: transportOverride.ReuseBypassSession, Effective: reuse,
			Inherited: transportOverride.ReuseBypassSession == nil, Mode: sessionMode,
		},
		ImageConnectionMode: ImageConnectionPolicyValue{
			Override: transportOverride.ImageConnectionMode, Effective: imageMode,
			Inherited: transportOverride.ImageConnectionMode == nil,
		},
		ImageProxy: ImageProxyState{
			OptedIn: optedIn, GatewayEnabled: snapshot.globals.ProxyEnabled, GatewayConfigured: proxyConfigured,
			EffectiveAvailable: optedIn && snapshot.globals.ProxyEnabled && proxyConfigured,
		},
		Routing: routing, ProfileKey: profileKey, Runtime: runtimeStatus(snapshot.runtime[sourceID]),
	}
}

func identity(source sourceengine.Source) SourceIdentity {
	return SourceIdentity{SourceID: source.ID, Name: source.Name, Language: source.Lang}
}

func effectiveBinding(sourceID int64, snapshot routingSnapshot) network.ResolvedBinding {
	if binding, ok := snapshot.Resolved[sourceID]; ok {
		return binding
	}
	return network.ResolvedBinding{SourceID: sourceID, FlareMode: network.FlareModeGlobal}
}

func resolveBypassSession(globalSession string, binding network.ResolvedBinding, override *bool) (bool, sourcetransport.BypassSessionMode) {
	if binding.FlareMode == network.FlareModeNone {
		return false, sourcetransport.BypassSessionDisabled
	}
	if override != nil && !*override {
		return false, sourcetransport.BypassSessionDisposable
	}
	session := globalSession
	if binding.FlareMode == network.FlareModeEndpoint && binding.Flare != nil {
		session = binding.Flare.Session
	}
	if session == "" {
		if override != nil && *override {
			return false, sourcetransport.BypassSessionDisabled
		}
		return false, sourcetransport.BypassSessionDisposable
	}
	return true, sourcetransport.BypassSessionReusable
}

func bypassEnabled(binding network.ResolvedBinding, globals globalSnapshot) bool {
	switch binding.FlareMode {
	case network.FlareModeNone:
		return false
	case network.FlareModeEndpoint:
		return binding.Flare != nil
	default:
		return globals.BypassEnabled && globals.BypassURL != ""
	}
}

func routingConfiguration(binding network.ResolvedBinding, names map[string]string) RoutingConfiguration {
	out := RoutingConfiguration{SocksMode: SocksModeGlobal, BypassMode: binding.FlareMode}
	if out.BypassMode == "" {
		out.BypassMode = network.FlareModeGlobal
	}
	if binding.Socks != nil {
		out.SocksMode = SocksModeEndpoint
		out.Socks = resolvedEndpoint(binding.Socks.ID, names)
	}
	if binding.FlareMode == network.FlareModeEndpoint && binding.Flare != nil {
		out.Bypass = resolvedEndpoint(binding.Flare.ID, names)
	}
	return out
}

func resolvedEndpoint(id string, names map[string]string) ResolvedEndpoint {
	endpointID := id
	result := ResolvedEndpoint{EndpointID: &endpointID}
	if value, ok := names[id]; ok {
		name := value
		result.Name = &name
	}
	return result
}

func runtimeStatus(intent sourcetransport.Intent) RuntimeStatus {
	status := RuntimePending
	if intent.DesiredRevision <= intent.AppliedRevision {
		status = RuntimeApplied
	}
	return RuntimeStatus{
		Status: status, DesiredRevision: intent.DesiredRevision, AppliedRevision: intent.AppliedRevision,
		LastApplyAttempt: intent.LastApplyAttempt, LastApplyError: intent.LastApplyError,
	}
}

func containsSourceID(ids []int64, sourceID int64) bool {
	index := sort.Search(len(ids), func(i int) bool { return ids[i] >= sourceID })
	return index < len(ids) && ids[index] == sourceID
}

func exceptionCount(sourceID int64, snapshot storesSnapshot) int {
	count := 0
	throughput := snapshot.throughput.Overrides[sourceID]
	if throughput.DownloadConcurrency != nil {
		count++
	}
	if throughput.ImageRequestDelay != nil {
		count++
	}
	transport := snapshot.transport.Overrides[sourceID]
	if transport.ReuseBypassSession != nil {
		count++
	}
	if transport.ImageConnectionMode != nil {
		count++
	}
	if containsSourceID(snapshot.globals.ProxySourceIDs, sourceID) {
		count++
	}
	binding, ok := snapshot.routing.Stored[sourceID]
	if !ok {
		return count
	}
	if binding.SocksEndpointID != nil {
		count++
	}
	if binding.FlareMode != network.FlareModeGlobal || binding.FlareEndpointID != nil {
		count++
	}
	return count
}

func deriveProfileKeys(sources []sourceengine.Source, routing routingSnapshot, overrides map[int64]sourcetransport.Override) map[int64]string {
	inputs := make([]engineroute.BindingInput, 0, len(sources))
	for _, source := range sources {
		binding := effectiveBinding(source.ID, routing)
		disableSession := overrides[source.ID].ReuseBypassSession != nil && !*overrides[source.ID].ReuseBypassSession
		inputs = append(inputs, engineroute.BindingInput{
			SourceID: source.ID, Socks: toSocks(binding.Socks), FlareMode: binding.FlareMode,
			Flare: toFlare(binding.Flare), DisableBypassSession: disableSession,
		})
	}
	out := make(map[int64]string)
	for _, profile := range engineroute.Derive(inputs) {
		for _, sourceID := range profile.SourceIDs {
			out[sourceID] = profile.Key
		}
	}
	return out
}

func toSocks(value *network.ResolvedSocks) *engineroute.SocksEndpoint {
	if value == nil {
		return nil
	}
	return &engineroute.SocksEndpoint{ID: value.ID, Host: value.Host, Port: value.Port, Version: value.Version, Username: value.Username, Password: value.Password}
}

func toFlare(value *network.ResolvedFlare) *engineroute.FlareEndpoint {
	if value == nil {
		return nil
	}
	return &engineroute.FlareEndpoint{ID: value.ID, URL: value.URL, Session: value.Session, SessionTTL: value.SessionTTL, Timeout: value.Timeout, AsResponseFallback: value.AsResponseFallback}
}
