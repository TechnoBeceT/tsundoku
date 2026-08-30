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

type IntegerPolicyValue struct {
	Override  *int
	Effective int
	Inherited bool
}

type DurationPolicyValue struct {
	Override  *time.Duration
	Effective time.Duration
	Inherited bool
}

type BooleanPolicyValue struct {
	Override  *bool
	Effective bool
	Inherited bool
}

type ImageConnectionPolicyValue struct {
	Override  *sourcetransport.ImageConnectionMode
	Effective sourcetransport.ImageConnectionMode
	Inherited bool
}

type BypassSessionPolicyValue struct {
	Override  *bool
	Effective bool
	Inherited bool
	Mode      sourcetransport.BypassSessionMode
}

type ProtectionConfiguration struct {
	WarmupInterval        time.Duration
	WarmupSlowThresholdMs int
	FailureThreshold      int
	SourceCooldown        time.Duration
	PolitenessDelay       time.Duration
}

type ImageProxyState struct {
	OptedIn            bool
	GatewayEnabled     bool
	GatewayConfigured  bool
	EffectiveAvailable bool
}

type ResolvedEndpoint struct {
	EndpointID *string
	Name       *string
}

type RoutingConfiguration struct {
	SocksMode  string
	Socks      ResolvedEndpoint
	BypassMode string
	Bypass     ResolvedEndpoint
}

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
