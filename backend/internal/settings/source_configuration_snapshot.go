package settings

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

var sourceConfigurationKeys = append(append([]string{}, runtimeConfigKeys...),
	KeyDownloadConcurrency,
	KeySourcesImageRequestDelay,
	KeyWarmupInterval,
	KeyWarmupSlowThresholdMs,
	KeySourcesFailureThreshold,
	KeySourcesCooldown,
	KeySourcesMinRequestDelay,
)

// SourceConfigurationSnapshot loads every global value used by the source
// configuration composer from one PostgreSQL statement snapshot.
func (s *Service) SourceConfigurationSnapshot(ctx context.Context) (SourceConfigurationSnapshot, error) {
	values, err := s.snapshotValues(ctx, "settings.SourceConfigurationSnapshot", sourceConfigurationKeys)
	if err != nil {
		return SourceConfigurationSnapshot{}, err
	}
	runtime, err := runtimeConfigFromValues(values)
	if err != nil {
		return SourceConfigurationSnapshot{}, fmt.Errorf("settings.SourceConfigurationSnapshot: %w", err)
	}
	downloadConcurrency, err := strconv.Atoi(values[KeyDownloadConcurrency])
	if err != nil {
		return SourceConfigurationSnapshot{}, fmt.Errorf("settings.SourceConfigurationSnapshot: parse %s: %w", KeyDownloadConcurrency, err)
	}
	imageRequestDelay, err := time.ParseDuration(values[KeySourcesImageRequestDelay])
	if err != nil {
		return SourceConfigurationSnapshot{}, fmt.Errorf("settings.SourceConfigurationSnapshot: parse %s: %w", KeySourcesImageRequestDelay, err)
	}
	warmupInterval, err := time.ParseDuration(values[KeyWarmupInterval])
	if err != nil {
		return SourceConfigurationSnapshot{}, fmt.Errorf("settings.SourceConfigurationSnapshot: parse %s: %w", KeyWarmupInterval, err)
	}
	warmupSlowThreshold, err := strconv.Atoi(values[KeyWarmupSlowThresholdMs])
	if err != nil {
		return SourceConfigurationSnapshot{}, fmt.Errorf("settings.SourceConfigurationSnapshot: parse %s: %w", KeyWarmupSlowThresholdMs, err)
	}
	failureThreshold, err := strconv.Atoi(values[KeySourcesFailureThreshold])
	if err != nil {
		return SourceConfigurationSnapshot{}, fmt.Errorf("settings.SourceConfigurationSnapshot: parse %s: %w", KeySourcesFailureThreshold, err)
	}
	sourceCooldown, err := time.ParseDuration(values[KeySourcesCooldown])
	if err != nil {
		return SourceConfigurationSnapshot{}, fmt.Errorf("settings.SourceConfigurationSnapshot: parse %s: %w", KeySourcesCooldown, err)
	}
	politenessDelay, err := time.ParseDuration(values[KeySourcesMinRequestDelay])
	if err != nil {
		return SourceConfigurationSnapshot{}, fmt.Errorf("settings.SourceConfigurationSnapshot: parse %s: %w", KeySourcesMinRequestDelay, err)
	}
	return SourceConfigurationSnapshot{
		Runtime: runtime, DownloadConcurrency: downloadConcurrency, ImageRequestDelay: imageRequestDelay,
		WarmupInterval: warmupInterval, WarmupSlowThresholdMs: warmupSlowThreshold,
		FailureThreshold: failureThreshold, SourceCooldown: sourceCooldown, PolitenessDelay: politenessDelay,
	}, nil
}
