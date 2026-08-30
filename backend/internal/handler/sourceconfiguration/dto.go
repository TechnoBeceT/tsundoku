package sourceconfiguration

import (
	"strconv"
	"time"

	configuration "github.com/technobecet/tsundoku/internal/sourceconfiguration"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

type SourceIdentityDTO struct {
	SourceID string `json:"sourceId"`
	Name     string `json:"name"`
	Language string `json:"language"`
}

type IntegerPolicyValueDTO struct {
	Override  *int `json:"override"`
	Effective int  `json:"effective"`
	Inherited bool `json:"inherited"`
}

type DurationPolicyValueDTO struct {
	Override  *string `json:"override"`
	Effective string  `json:"effective"`
	Inherited bool    `json:"inherited"`
}

type ImageConnectionPolicyValueDTO struct {
	Override  *sourcetransport.ImageConnectionMode `json:"override"`
	Effective sourcetransport.ImageConnectionMode  `json:"effective"`
	Inherited bool                                 `json:"inherited"`
}

type BypassSessionPolicyValueDTO struct {
	Override  *bool                             `json:"override"`
	Effective bool                              `json:"effective"`
	Inherited bool                              `json:"inherited"`
	Mode      sourcetransport.BypassSessionMode `json:"mode"`
}

type ProtectionConfigurationDTO struct {
	WarmupInterval        string `json:"warmupInterval"`
	WarmupSlowThresholdMs int    `json:"warmupSlowThresholdMs"`
	FailureThreshold      int    `json:"failureThreshold"`
	SourceCooldown        string `json:"sourceCooldown"`
	PolitenessDelay       string `json:"politenessDelay"`
}

type ImageProxyStateDTO struct {
	OptedIn            bool `json:"optedIn"`
	GatewayEnabled     bool `json:"gatewayEnabled"`
	GatewayConfigured  bool `json:"gatewayConfigured"`
	EffectiveAvailable bool `json:"effectiveAvailable"`
}

type ResolvedEndpointDTO struct {
	EndpointID *string `json:"endpointId"`
	Name       *string `json:"name"`
}

type RoutingConfigurationDTO struct {
	SocksMode  string              `json:"socksMode"`
	Socks      ResolvedEndpointDTO `json:"socks"`
	BypassMode string              `json:"bypassMode"`
	Bypass     ResolvedEndpointDTO `json:"bypass"`
}

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
	ImageProxy          ImageProxyStateDTO            `json:"imageProxy"`
	Routing             RoutingConfigurationDTO       `json:"routing"`
	ProfileKey          string                        `json:"profileKey"`
	Runtime             RuntimeStatusDTO              `json:"runtime"`
}

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
			Override: value.ReuseBypassSession.Override, Effective: value.ReuseBypassSession.Effective,
			Inherited: value.ReuseBypassSession.Inherited, Mode: value.ReuseBypassSession.Mode,
		},
		ImageConnectionMode: ImageConnectionPolicyValueDTO{
			Override: value.ImageConnectionMode.Override, Effective: value.ImageConnectionMode.Effective, Inherited: value.ImageConnectionMode.Inherited,
		},
		ImageProxy: ImageProxyStateDTO{
			OptedIn: value.ImageProxy.OptedIn, GatewayEnabled: value.ImageProxy.GatewayEnabled,
			GatewayConfigured: value.ImageProxy.GatewayConfigured, EffectiveAvailable: value.ImageProxy.EffectiveAvailable,
		},
		Routing: RoutingConfigurationDTO{
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
