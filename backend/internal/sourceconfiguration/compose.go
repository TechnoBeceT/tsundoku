package sourceconfiguration

import (
	"sort"

	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/runtimepolicy"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

func compose(source sourceengine.Source, snapshot storesSnapshot, profileKey string) Configuration {
	sourceID := source.ID
	throughputOverride := snapshot.throughput.Overrides[sourceID]
	throughputEffective := sourcethroughput.ApplyDefaults(sourcethroughput.Effective{
		DownloadConcurrency: snapshot.globals.DownloadConcurrency,
		ImageRequestDelay:   snapshot.globals.ImageRequestDelay,
	}, throughputOverride)
	transportOverride := snapshot.transport.Overrides[sourceID]
	imageMode := snapshot.transport.DefaultImageConnectionMode
	if transportOverride.ImageConnectionMode != nil {
		imageMode = *transportOverride.ImageConnectionMode
	}
	binding := effectiveBinding(sourceID, snapshot.routing)
	kcefPolicy := kcefPolicy(transportOverride)
	kcefEnabled := effectiveKCEFEnabled(kcefPolicy, binding)
	storedBinding, configured := snapshot.routing.Stored[sourceID]
	reuseGlobal, _ := resolveBypassSession(snapshot.globals.BypassSession, binding, nil)
	reuse, sessionMode := resolveBypassSession(snapshot.globals.BypassSession, binding, transportOverride.ReuseBypassSession)
	optedIn := containsSourceID(snapshot.globals.ProxySourceIDs, sourceID)
	proxyConfigured := snapshot.globals.ProxyURL != ""
	routing := routingConfiguration(binding, storedBinding, configured, snapshot.routing.EndpointNames)
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
			Override: transportOverride.ReuseBypassSession, Global: reuseGlobal, Effective: reuse,
			Inherited: transportOverride.ReuseBypassSession == nil, Mode: sessionMode,
		},
		ImageConnectionMode: ImageConnectionPolicyValue{
			Override: transportOverride.ImageConnectionMode, Global: snapshot.transport.DefaultImageConnectionMode, Effective: imageMode,
			Inherited: transportOverride.ImageConnectionMode == nil,
		},
		KCEF: KCEFPolicyValue{
			Override: transportOverride.KCEFPolicy, Global: runtimepolicy.KCEFPolicyAuto, Effective: kcefPolicy,
			Inherited: transportOverride.KCEFPolicy == nil, Enabled: kcefEnabled,
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

func routingConfiguration(binding network.ResolvedBinding, stored network.ConfigurationBinding, configured bool, names map[string]string) RoutingConfiguration {
	out := RoutingConfiguration{
		Stored:     storedRoutingConfiguration(stored, configured, names),
		SocksMode:  SocksModeGlobal,
		BypassMode: binding.FlareMode,
	}
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

func storedRoutingConfiguration(binding network.ConfigurationBinding, configured bool, names map[string]string) StoredRoutingConfiguration {
	out := StoredRoutingConfiguration{Configured: configured, SocksMode: SocksModeGlobal, BypassMode: binding.FlareMode}
	if out.BypassMode == "" {
		out.BypassMode = network.FlareModeGlobal
	}
	if binding.SocksEndpointID != nil {
		out.SocksMode = SocksModeEndpoint
		out.Socks = storedEndpoint(binding.SocksEndpointID, names)
	}
	if binding.FlareEndpointID != nil {
		out.Bypass = storedEndpoint(binding.FlareEndpointID, names)
	}
	return out
}

func storedEndpoint(id *string, names map[string]string) ResolvedEndpoint {
	if id == nil {
		return ResolvedEndpoint{}
	}
	return resolvedEndpoint(*id, names)
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
	if transport.KCEFPolicy != nil {
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

func deriveProfileKeys(sources []sourceengine.Source, routing routingSnapshot, overrides map[int64]sourcetransport.Override, defaultKCEFEnabled bool) map[int64]string {
	inputs := make([]engineroute.BindingInput, 0, len(sources))
	for _, source := range sources {
		binding := effectiveBinding(source.ID, routing)
		disableSession := overrides[source.ID].ReuseBypassSession != nil && !*overrides[source.ID].ReuseBypassSession
		inputs = append(inputs, engineroute.BindingInput{
			SourceID: source.ID, Socks: toSocks(binding.Socks), FlareMode: binding.FlareMode,
			Flare: toFlare(binding.Flare), DisableBypassSession: disableSession,
			KCEFEnabled: effectiveKCEFEnabled(kcefPolicy(overrides[source.ID]), binding),
		})
	}
	out := make(map[int64]string)
	for _, profile := range engineroute.Derive(defaultKCEFEnabled, inputs) {
		for _, sourceID := range profile.SourceIDs {
			out[sourceID] = profile.Key
		}
	}
	return out
}

func effectiveKCEFEnabled(policy runtimepolicy.KCEFPolicy, binding network.ResolvedBinding) bool {
	enabled, err := runtimepolicy.ResolveKCEF(policy, binding.Socks != nil, binding.FlareMode)
	if err != nil {
		return false
	}
	return enabled
}

func kcefPolicy(override sourcetransport.Override) runtimepolicy.KCEFPolicy {
	if override.KCEFPolicy == nil {
		return runtimepolicy.KCEFPolicyAuto
	}
	return *override.KCEFPolicy
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
