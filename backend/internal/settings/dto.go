package settings

import "time"

// SettingDTO is one row of the settings API: the resolved current value, the
// config default, and the type + unit metadata the FE uses to render the right
// input. value is the DB override when present, otherwise default.
type SettingDTO struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Default string `json:"default"`
	Type    string `json:"type"`
	Unit    string `json:"unit"`
}

// KeyValue is one requested update in a Set/SetMany call: the tunable key and its
// new raw value (validated against the key's allowlist entry before it is stored).
type KeyValue struct {
	Key   string
	Value string
}

// RuntimeConfigSnapshot is one committed view of every global setting pushed
// to engine-host instances. The settings service loads this complete group with
// one query so a concurrent SetMany transaction can be observed only before or
// after its commit, never as an impossible mixture of both states.
type RuntimeConfigSnapshot struct {
	FlareSolverrEnabled          bool
	FlareSolverrURL              string
	FlareSolverrTimeout          int
	FlareSolverrSessionName      string
	FlareSolverrSessionTTL       int
	FlareSolverrResponseFallback bool
	EngineSocksEnabled           bool
	EngineSocksHost              string
	EngineSocksPort              int
	EngineSocksVersion           int
	ImpersonateEnabled           bool
	ImpersonateURL               string
	ImpersonateSources           []int64
}

// SourceConfigurationSnapshot is one committed view of every global value
// needed to compose source configuration. Runtime, protection, and throughput
// defaults are loaded by one settings query so callers cannot observe a mixture
// across concurrent setting commits; read failures are returned.
type SourceConfigurationSnapshot struct {
	Runtime               RuntimeConfigSnapshot
	DownloadConcurrency   int
	ImageRequestDelay     time.Duration
	WarmupInterval        time.Duration
	WarmupSlowThresholdMs int
	FailureThreshold      int
	SourceCooldown        time.Duration
	PolitenessDelay       time.Duration
}

// RuntimeIntent is the durable revision state for the global engine config.
// DesiredRevision advances atomically with runtime setting writes;
// AppliedRevision advances only after that exact revision converges.
type RuntimeIntent struct {
	DesiredRevision  int64
	AppliedRevision  int64
	LastApplyAttempt *time.Time
	LastApplyError   string
}
