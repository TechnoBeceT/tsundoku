package sourceconfiguration

import (
	"time"

	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

const (
	SocksModeGlobal   = "global"
	SocksModeEndpoint = "endpoint"
	RuntimeApplied    = "applied"
	RuntimePending    = "pending"
)

// SourceIdentity is the transport-independent identity of one live source.
type SourceIdentity struct {
	SourceID int64
	Name     string
	Language string
}

// IntegerPolicyValue resolves an optional per-source integer override.
type IntegerPolicyValue struct {
	Override  *int
	Effective int
	Inherited bool
}

// DurationPolicyValue resolves an optional per-source duration override.
type DurationPolicyValue struct {
	Override  *time.Duration
	Effective time.Duration
	Inherited bool
}

// BooleanPolicyValue resolves an optional per-source boolean override.
type BooleanPolicyValue struct {
	Override  *bool
	Effective bool
	Inherited bool
}

// ImageConnectionPolicyValue resolves the source image-connection policy.
type ImageConnectionPolicyValue struct {
	Override  *sourcetransport.ImageConnectionMode
	Global    sourcetransport.ImageConnectionMode
	Effective sourcetransport.ImageConnectionMode
	Inherited bool
}

// BypassSessionPolicyValue resolves bypass-session reuse and its runtime mode.
type BypassSessionPolicyValue struct {
	Override  *bool
	Global    bool
	Effective bool
	Inherited bool
	Mode      sourcetransport.BypassSessionMode
}

// ProtectionConfiguration contains effective global source-protection values.
type ProtectionConfiguration struct {
	WarmupInterval        time.Duration
	WarmupSlowThresholdMs int
	FailureThreshold      int
	SourceCooldown        time.Duration
	PolitenessDelay       time.Duration
}

// ImageProxyState describes image-proxy selection and runtime availability.
type ImageProxyState struct {
	OptedIn            bool
	GatewayEnabled     bool
	GatewayConfigured  bool
	EffectiveAvailable bool
}

// ResolvedEndpoint identifies an available routing endpoint for display.
type ResolvedEndpoint struct {
	EndpointID *string
	Name       *string
}

// RoutingConfiguration contains effective SOCKS and bypass routing choices.
type RoutingConfiguration struct {
	SocksMode  string
	Socks      ResolvedEndpoint
	BypassMode string
	Bypass     ResolvedEndpoint
}

// RuntimeStatus reports the durable apply state for one source profile.
type RuntimeStatus struct {
	Status           string
	DesiredRevision  int64
	AppliedRevision  int64
	LastApplyAttempt *time.Time
	LastApplyError   string
}

// Configuration is one source's fully composed effective configuration.
type Configuration struct {
	Source              SourceIdentity
	DownloadConcurrency IntegerPolicyValue
	ImageRequestDelay   DurationPolicyValue
	Protection          ProtectionConfiguration
	BypassEnabled       bool
	ReuseBypassSession  BypassSessionPolicyValue
	ImageConnectionMode ImageConnectionPolicyValue
	ImageProxy          ImageProxyState
	Routing             RoutingConfiguration
	ProfileKey          string
	Runtime             RuntimeStatus
}

// Summary identifies a source with at least one field-level exception.
type Summary struct {
	Source         SourceIdentity
	ExceptionCount int
	Runtime        RuntimeStatus
}
