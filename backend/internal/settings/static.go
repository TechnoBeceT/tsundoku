package settings

import (
	"context"
	"time"
)

// Static is a fixed-value tunable provider: every accessor returns the value it
// was constructed with, ignoring the DB. It is for wiring or tests that do not
// need runtime tuning — it satisfies the same accessor surface as *Service
// (download.RetrySettings, job.Intervals, refresh.Concurrency, and the
// series stale-grace resolver) so either can be threaded into a consumer.
type Static struct {
	Download     time.Duration
	DownloadConc int
	Refresh      time.Duration
	Concurrency  int
	Retries      int
	Backoff      time.Duration
	StaleGrace   int
	ExtCheck     time.Duration
	WarmupIv     time.Duration
	WarmupSlow   int
	// SearchCacheIv / ChapterCacheIv back the interactive cache TTL accessors;
	// 0 disables the corresponding cache.
	SearchCacheIv  time.Duration
	ChapterCacheIv time.Duration
	// SourcesFailureThresh / SourcesCooldownIv / SourcesMinDelay back the
	// source-politeness gate (internal/sourcegate) accessors below.
	SourcesFailureThresh int
	SourcesCooldownIv    time.Duration
	SourcesMinDelay      time.Duration
	// SuppressParts backs the SuppressSplitParts accessor.
	SuppressParts bool
	// TrackRetryIv backs the TrackRetryInterval accessor.
	TrackRetryIv time.Duration
	// AutoUpdate backs the AutoUpdateTrack accessor.
	AutoUpdate bool
	// MetadataAutoIdentifyFlag backs the MetadataAutoIdentify accessor.
	MetadataAutoIdentifyFlag bool
	// FlareSolverrOn..FlareSolverrFallback back the FlareSolverr* accessors
	// below (QCAT-238, Tsundoku-owned Cloudflare-bypass config).
	FlareSolverrOn          bool
	FlareSolverrURLValue    string
	FlareSolverrTimeoutSecs int
	FlareSolverrSession     string
	FlareSolverrTTLMinutes  int
	FlareSolverrFallback    bool
	// NotificationsOn backs the NotificationsEnabled accessor.
	NotificationsOn bool
	// EngineSocksOn..EngineSocksVer back the EngineSocks* accessors below
	// (engine SOCKS-proxy config, mirrors the FlareSolverr* fields' pattern).
	EngineSocksOn     bool
	EngineSocksAddr   string
	EngineSocksPortNo int
	EngineSocksVer    int
}

// DownloadInterval returns the fixed download ticker period.
func (s Static) DownloadInterval(context.Context) time.Duration { return s.Download }

// DownloadConcurrency returns the fixed per-source download concurrency cap.
func (s Static) DownloadConcurrency(context.Context) int { return s.DownloadConc }

// RefreshInterval returns the fixed discovery-sweep period.
func (s Static) RefreshInterval(context.Context) time.Duration { return s.Refresh }

// RefreshConcurrency returns the fixed parallel-refetch bound.
func (s Static) RefreshConcurrency(context.Context) int { return s.Concurrency }

// MaxRetries returns the fixed failed-download retry budget.
func (s Static) MaxRetries(context.Context) int { return s.Retries }

// RetryBackoff returns the fixed base retry backoff delay.
func (s Static) RetryBackoff(context.Context) time.Duration { return s.Backoff }

// StaleGraceDays returns the fixed source-health stale threshold.
func (s Static) StaleGraceDays(context.Context) int { return s.StaleGrace }

// ExtensionCheckInterval returns the fixed extension-check ticker period; 0 = disabled.
func (s Static) ExtensionCheckInterval(context.Context) time.Duration { return s.ExtCheck }

// WarmupInterval returns the fixed warm-up ticker period; 0 = disabled.
func (s Static) WarmupInterval(context.Context) time.Duration { return s.WarmupIv }

// WarmupSlowThresholdMs returns the fixed slow-latency threshold in milliseconds.
func (s Static) WarmupSlowThresholdMs(context.Context) int { return s.WarmupSlow }

// SearchCacheTTL returns the fixed interactive Search cache lifetime; 0 disables it.
func (s Static) SearchCacheTTL(context.Context) time.Duration { return s.SearchCacheIv }

// ChapterCacheTTL returns the fixed interactive FetchChapters cache lifetime; 0 disables it.
func (s Static) ChapterCacheTTL(context.Context) time.Duration { return s.ChapterCacheIv }

// SourcesFailureThreshold returns the fixed circuit-breaker trip threshold.
func (s Static) SourcesFailureThreshold(context.Context) int { return s.SourcesFailureThresh }

// SourcesCooldown returns the fixed circuit-breaker cooldown duration.
func (s Static) SourcesCooldown(context.Context) time.Duration { return s.SourcesCooldownIv }

// SourcesMinRequestDelay returns the fixed per-source politeness delay; 0 disables it.
func (s Static) SourcesMinRequestDelay(context.Context) time.Duration { return s.SourcesMinDelay }

// SuppressSplitParts returns the fixed fractional-part-suppression flag.
func (s Static) SuppressSplitParts(context.Context) bool { return s.SuppressParts }

// TrackRetryInterval returns the fixed tracker-push retry-queue drain period.
func (s Static) TrackRetryInterval(context.Context) time.Duration { return s.TrackRetryIv }

// AutoUpdateTrack returns the fixed reading-triggered tracker-sync toggle.
func (s Static) AutoUpdateTrack(context.Context) bool { return s.AutoUpdate }

// MetadataAutoIdentify returns the fixed metadata-engine auto-identify toggle.
func (s Static) MetadataAutoIdentify(context.Context) bool { return s.MetadataAutoIdentifyFlag }

// FlareSolverrEnabled returns the fixed FlareSolverr enabled toggle.
func (s Static) FlareSolverrEnabled(context.Context) bool { return s.FlareSolverrOn }

// FlareSolverrURL returns the fixed FlareSolverr endpoint.
func (s Static) FlareSolverrURL(context.Context) string { return s.FlareSolverrURLValue }

// FlareSolverrTimeout returns the fixed per-request solve timeout in seconds.
func (s Static) FlareSolverrTimeout(context.Context) int { return s.FlareSolverrTimeoutSecs }

// FlareSolverrSessionName returns the fixed FlareSolverr session identifier.
func (s Static) FlareSolverrSessionName(context.Context) string { return s.FlareSolverrSession }

// FlareSolverrSessionTTL returns the fixed session time-to-live in minutes.
func (s Static) FlareSolverrSessionTTL(context.Context) int { return s.FlareSolverrTTLMinutes }

// FlareSolverrResponseFallback returns the fixed asResponseFallback mirror flag.
func (s Static) FlareSolverrResponseFallback(context.Context) bool { return s.FlareSolverrFallback }

// NotificationsEnabled returns the fixed new-chapter notifications toggle.
func (s Static) NotificationsEnabled(context.Context) bool { return s.NotificationsOn }

// EngineSocksEnabled returns the fixed engine SOCKS-proxy enabled toggle.
func (s Static) EngineSocksEnabled(context.Context) bool { return s.EngineSocksOn }

// EngineSocksHost returns the fixed engine SOCKS-proxy hostname or IP.
func (s Static) EngineSocksHost(context.Context) string { return s.EngineSocksAddr }

// EngineSocksPort returns the fixed engine SOCKS-proxy port.
func (s Static) EngineSocksPort(context.Context) int { return s.EngineSocksPortNo }

// EngineSocksVersion returns the fixed engine SOCKS protocol version.
func (s Static) EngineSocksVersion(context.Context) int { return s.EngineSocksVer }
