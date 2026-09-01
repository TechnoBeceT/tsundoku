package sourceconfiguration

import (
	"strconv"
	"time"

	"github.com/technobecet/tsundoku/internal/runtimepolicy"
	configuration "github.com/technobecet/tsundoku/internal/sourceconfiguration"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

// SourceIdentityDTO is the source identity wire projection.
type SourceIdentityDTO struct {
	SourceID string `json:"sourceId"`
	Name     string `json:"name"`
	Language string `json:"language"`
}

// IntegerPolicyValueDTO is an inherited-or-overridden integer policy value.
type IntegerPolicyValueDTO struct {
	Override  *int `json:"override"`
	Effective int  `json:"effective"`
	Inherited bool `json:"inherited"`
}

// DurationPolicyValueDTO is an inherited-or-overridden duration policy value.
type DurationPolicyValueDTO struct {
	Override  *string `json:"override"`
	Effective string  `json:"effective"`
	Inherited bool    `json:"inherited"`
}

// ImageConnectionPolicyValueDTO is the resolved image-connection policy value.
type ImageConnectionPolicyValueDTO struct {
	Override  *sourcetransport.ImageConnectionMode `json:"override"`
	Global    sourcetransport.ImageConnectionMode  `json:"global"`
	Effective sourcetransport.ImageConnectionMode  `json:"effective"`
	Inherited bool                                 `json:"inherited"`
}

// KCEFPolicyValueDTO is the resolved embedded-browser policy wire projection.
type KCEFPolicyValueDTO struct {
	Override  *runtimepolicy.KCEFPolicy `json:"override"`
	Global    runtimepolicy.KCEFPolicy  `json:"global"`
	Effective runtimepolicy.KCEFPolicy  `json:"effective"`
	Inherited bool                      `json:"inherited"`
	Enabled   bool                      `json:"enabled"`
}

// BypassSessionPolicyValueDTO is the resolved bypass-session policy value.
type BypassSessionPolicyValueDTO struct {
	Override  *bool                             `json:"override"`
	Global    bool                              `json:"global"`
	Effective bool                              `json:"effective"`
	Inherited bool                              `json:"inherited"`
	Mode      sourcetransport.BypassSessionMode `json:"mode"`
}

// ProtectionConfigurationDTO is the source-protection wire projection.
type ProtectionConfigurationDTO struct {
	WarmupInterval        string `json:"warmupInterval"`
	WarmupSlowThresholdMs int    `json:"warmupSlowThresholdMs"`
	FailureThreshold      int    `json:"failureThreshold"`
	SourceCooldown        string `json:"sourceCooldown"`
	PolitenessDelay       string `json:"politenessDelay"`
}

// ImageProxyStateDTO is the image-proxy availability wire projection.
type ImageProxyStateDTO struct {
	OptedIn            bool `json:"optedIn"`
	GatewayEnabled     bool `json:"gatewayEnabled"`
	GatewayConfigured  bool `json:"gatewayConfigured"`
	EffectiveAvailable bool `json:"effectiveAvailable"`
}

// ResolvedEndpointDTO identifies a resolved routing endpoint on the wire.
type ResolvedEndpointDTO struct {
	EndpointID *string `json:"endpointId"`
	Name       *string `json:"name"`
}

// StoredRoutingConfigurationDTO is the persisted binding projection before
// endpoint availability is resolved.
type StoredRoutingConfigurationDTO struct {
	Configured bool                `json:"configured"`
	SocksMode  string              `json:"socksMode"`
	Socks      ResolvedEndpointDTO `json:"socks"`
	BypassMode string              `json:"bypassMode"`
	Bypass     ResolvedEndpointDTO `json:"bypass"`
}

// RoutingConfigurationDTO carries both persisted and effective routing.
type RoutingConfigurationDTO struct {
	Stored     StoredRoutingConfigurationDTO `json:"stored"`
	SocksMode  string                        `json:"socksMode"`
	Socks      ResolvedEndpointDTO           `json:"socks"`
	BypassMode string                        `json:"bypassMode"`
	Bypass     ResolvedEndpointDTO           `json:"bypass"`
}

// RuntimeStatusDTO is one source profile's durable apply state on the wire.
type RuntimeStatusDTO struct {
	Status           string     `json:"status"`
	DesiredRevision  int64      `json:"desiredRevision"`
	AppliedRevision  int64      `json:"appliedRevision"`
	LastApplyAttempt *time.Time `json:"lastApplyAttempt"`
	LastApplyError   string     `json:"lastApplyError"`
}

// ConfigurationDTO is the exact SourceEffectiveConfiguration wire shape.
type ConfigurationDTO struct {
	Source              SourceIdentityDTO             `json:"source"`
	DownloadConcurrency IntegerPolicyValueDTO         `json:"downloadConcurrency"`
	ImageRequestDelay   DurationPolicyValueDTO        `json:"imageRequestDelay"`
	Protection          ProtectionConfigurationDTO    `json:"protection"`
	BypassEnabled       bool                          `json:"bypassEnabled"`
	ReuseBypassSession  BypassSessionPolicyValueDTO   `json:"reuseBypassSession"`
	ImageConnectionMode ImageConnectionPolicyValueDTO `json:"imageConnectionMode"`
	KCEF                KCEFPolicyValueDTO            `json:"kcef"`
	ImageProxy          ImageProxyStateDTO            `json:"imageProxy"`
	Routing             RoutingConfigurationDTO       `json:"routing"`
	ProfileKey          string                        `json:"profileKey"`
	Runtime             RuntimeStatusDTO              `json:"runtime"`
}

// SummaryDTO is the wire projection for one source with exceptions.
type SummaryDTO struct {
	Source         SourceIdentityDTO `json:"source"`
	ExceptionCount int               `json:"exceptionCount"`
	Runtime        RuntimeStatusDTO  `json:"runtime"`
}

func newConfigurationDTO(value configuration.Configuration) ConfigurationDTO {
	return ConfigurationDTO{
		Source:              newSourceIdentityDTO(value.Source),
		DownloadConcurrency: IntegerPolicyValueDTO{Override: value.DownloadConcurrency.Override, Effective: value.DownloadConcurrency.Effective, Inherited: value.DownloadConcurrency.Inherited},
		ImageRequestDelay: DurationPolicyValueDTO{
			Override: durationPointer(value.ImageRequestDelay.Override), Effective: value.ImageRequestDelay.Effective.String(), Inherited: value.ImageRequestDelay.Inherited,
		},
		Protection: ProtectionConfigurationDTO{
			WarmupInterval: value.Protection.WarmupInterval.String(), WarmupSlowThresholdMs: value.Protection.WarmupSlowThresholdMs,
			FailureThreshold: value.Protection.FailureThreshold, SourceCooldown: value.Protection.SourceCooldown.String(),
			PolitenessDelay: value.Protection.PolitenessDelay.String(),
		},
		BypassEnabled: value.BypassEnabled,
		ReuseBypassSession: BypassSessionPolicyValueDTO{
			Override: value.ReuseBypassSession.Override, Global: value.ReuseBypassSession.Global, Effective: value.ReuseBypassSession.Effective,
			Inherited: value.ReuseBypassSession.Inherited, Mode: value.ReuseBypassSession.Mode,
		},
		ImageConnectionMode: ImageConnectionPolicyValueDTO{
			Override: value.ImageConnectionMode.Override, Global: value.ImageConnectionMode.Global,
			Effective: value.ImageConnectionMode.Effective, Inherited: value.ImageConnectionMode.Inherited,
		},
		KCEF: KCEFPolicyValueDTO{
			Override: value.KCEF.Override, Global: value.KCEF.Global, Effective: value.KCEF.Effective,
			Inherited: value.KCEF.Inherited, Enabled: value.KCEF.Enabled,
		},
		ImageProxy: ImageProxyStateDTO{
			OptedIn: value.ImageProxy.OptedIn, GatewayEnabled: value.ImageProxy.GatewayEnabled,
			GatewayConfigured: value.ImageProxy.GatewayConfigured, EffectiveAvailable: value.ImageProxy.EffectiveAvailable,
		},
		Routing: RoutingConfigurationDTO{
			Stored: StoredRoutingConfigurationDTO{
				Configured: value.Routing.Stored.Configured,
				SocksMode:  value.Routing.Stored.SocksMode,
				Socks:      ResolvedEndpointDTO{EndpointID: value.Routing.Stored.Socks.EndpointID, Name: value.Routing.Stored.Socks.Name},
				BypassMode: value.Routing.Stored.BypassMode,
				Bypass:     ResolvedEndpointDTO{EndpointID: value.Routing.Stored.Bypass.EndpointID, Name: value.Routing.Stored.Bypass.Name},
			},
			SocksMode:  value.Routing.SocksMode,
			Socks:      ResolvedEndpointDTO{EndpointID: value.Routing.Socks.EndpointID, Name: value.Routing.Socks.Name},
			BypassMode: value.Routing.BypassMode,
			Bypass:     ResolvedEndpointDTO{EndpointID: value.Routing.Bypass.EndpointID, Name: value.Routing.Bypass.Name},
		},
		ProfileKey: value.ProfileKey,
		Runtime:    newRuntimeStatusDTO(value.Runtime),
	}
}

func newSummaryDTO(value configuration.Summary) SummaryDTO {
	return SummaryDTO{Source: newSourceIdentityDTO(value.Source), ExceptionCount: value.ExceptionCount, Runtime: newRuntimeStatusDTO(value.Runtime)}
}

func newSummaryDTOs(values []configuration.Summary) []SummaryDTO {
	out := make([]SummaryDTO, 0, len(values))
	for _, value := range values {
		out = append(out, newSummaryDTO(value))
	}
	return out
}

func newSourceIdentityDTO(value configuration.SourceIdentity) SourceIdentityDTO {
	return SourceIdentityDTO{SourceID: strconv.FormatInt(value.SourceID, 10), Name: value.Name, Language: value.Language}
}

func newRuntimeStatusDTO(value configuration.RuntimeStatus) RuntimeStatusDTO {
	return RuntimeStatusDTO{
		Status: value.Status, DesiredRevision: value.DesiredRevision, AppliedRevision: value.AppliedRevision,
		LastApplyAttempt: value.LastApplyAttempt, LastApplyError: value.LastApplyError,
	}
}

func durationPointer(value *time.Duration) *string {
	if value == nil {
		return nil
	}
	formatted := value.String()
	return &formatted
}
