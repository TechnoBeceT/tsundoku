package sourcethroughput

import (
	"context"
	"fmt"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entpolicy "github.com/technobecet/tsundoku/internal/ent/sourcethroughputpolicy"
)

// Override holds the independently optional values stored for one source.
type Override struct {
	DownloadConcurrency *int
	ImageRequestDelay   *time.Duration
}

// Effective is the fully resolved policy for one source.
type Effective struct {
	DownloadConcurrency int
	ImageRequestDelay   time.Duration
}

// Defaults supplies runtime global settings used when an override is absent.
type Defaults interface {
	DownloadConcurrency(context.Context) int
	ImageRequestDelay(context.Context) time.Duration
}

// PatchOperation selects how one field in a Patch changes its stored override.
type PatchOperation uint8

const (
	// PatchKeep leaves the stored field unchanged.
	PatchKeep PatchOperation = iota
	// PatchSet stores PatchField.Value as the field's override.
	PatchSet
	// PatchClear removes the field's override so it inherits its default.
	PatchClear
)

// PatchField represents keep, set, and clear without using pointer nil as both
// omission and inheritance.
type PatchField[T any] struct {
	Operation PatchOperation
	Value     T
}

// Set returns a patch field that stores value as an override.
func Set[T any](value T) PatchField[T] {
	return PatchField[T]{Operation: PatchSet, Value: value}
}

// Clear returns a patch field that removes an override.
func Clear[T any]() PatchField[T] {
	return PatchField[T]{Operation: PatchClear}
}

// Patch changes the two stored overrides independently. A zero Patch keeps
// both fields unchanged.
type Patch struct {
	DownloadConcurrency PatchField[int]
	ImageRequestDelay   PatchField[time.Duration]
}

// Service persists and resolves per-source throughput overrides.
type Service struct {
	client   *ent.Client
	defaults Defaults
}

// NewService constructs a source-throughput policy service.
func NewService(client *ent.Client, defaults Defaults) *Service {
	return &Service{client: client, defaults: defaults}
}

// Snapshot loads every stored source override in one query.
func (s *Service) Snapshot(ctx context.Context) (map[int64]Override, error) {
	rows, err := s.client.SourceThroughputPolicy.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("sourcethroughput.Snapshot: query policies: %w", err)
	}

	snapshot := make(map[int64]Override, len(rows))
	for _, row := range rows {
		snapshot[row.SourceID] = overrideFromRow(row)
	}
	return snapshot, nil
}

// Resolve returns the source's stored overrides applied to the current runtime
// defaults. Defaults are read on every call so global setting changes hot-reload.
func (s *Service) Resolve(ctx context.Context, sourceID int64) (Effective, error) {
	row, err := s.client.SourceThroughputPolicy.Query().
		Where(entpolicy.SourceID(sourceID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return Effective{}, fmt.Errorf("sourcethroughput.Resolve: query source %d: %w", sourceID, err)
	}

	var stored Override
	if err == nil {
		stored = overrideFromRow(row)
	}
	return s.resolve(ctx, stored), nil
}

// ImageRequestDelay returns the effective delay through the same resolution
// path as Resolve. It is the narrow adapter used by image-request pacing.
func (s *Service) ImageRequestDelay(ctx context.Context, sourceID int64) (time.Duration, error) {
	effective, err := s.Resolve(ctx, sourceID)
	if err != nil {
		return 0, err
	}
	return effective.ImageRequestDelay, nil
}

// Update applies a validated partial patch. Clearing the final override deletes
// the row so fully inherited policy has a canonical absence representation.
func (s *Service) Update(ctx context.Context, sourceID int64, patch Patch) (Override, error) {
	if err := validatePatch(patch); err != nil {
		return Override{}, err
	}

	row, err := s.client.SourceThroughputPolicy.Query().
		Where(entpolicy.SourceID(sourceID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return Override{}, fmt.Errorf("sourcethroughput.Update: query source %d: %w", sourceID, err)
	}

	var next Override
	if err == nil {
		next = overrideFromRow(row)
	}
	applyPatch(&next, patch)

	if next.DownloadConcurrency == nil && next.ImageRequestDelay == nil {
		if err == nil {
			if deleteErr := s.client.SourceThroughputPolicy.DeleteOneID(row.ID).Exec(ctx); deleteErr != nil {
				return Override{}, fmt.Errorf("sourcethroughput.Update: delete source %d: %w", sourceID, deleteErr)
			}
		}
		return Override{}, nil
	}

	if ent.IsNotFound(err) {
		delayMs := durationMillis(next.ImageRequestDelay)
		created, createErr := s.client.SourceThroughputPolicy.Create().
			SetSourceID(sourceID).
			SetNillableDownloadConcurrency(next.DownloadConcurrency).
			SetNillableImageRequestDelayMs(delayMs).
			Save(ctx)
		if createErr != nil {
			return Override{}, fmt.Errorf("sourcethroughput.Update: create source %d: %w", sourceID, createErr)
		}
		return overrideFromRow(created), nil
	}

	update := row.Update()
	if next.DownloadConcurrency == nil {
		update.ClearDownloadConcurrency()
	} else {
		update.SetDownloadConcurrency(*next.DownloadConcurrency)
	}
	if next.ImageRequestDelay == nil {
		update.ClearImageRequestDelayMs()
	} else {
		update.SetImageRequestDelayMs(next.ImageRequestDelay.Milliseconds())
	}
	updated, updateErr := update.Save(ctx)
	if updateErr != nil {
		return Override{}, fmt.Errorf("sourcethroughput.Update: update source %d: %w", sourceID, updateErr)
	}
	return overrideFromRow(updated), nil
}

func (s *Service) resolve(ctx context.Context, stored Override) Effective {
	effective := Effective{
		DownloadConcurrency: s.defaults.DownloadConcurrency(ctx),
		ImageRequestDelay:   s.defaults.ImageRequestDelay(ctx),
	}
	if stored.DownloadConcurrency != nil {
		effective.DownloadConcurrency = *stored.DownloadConcurrency
	}
	if stored.ImageRequestDelay != nil {
		effective.ImageRequestDelay = *stored.ImageRequestDelay
	}
	return effective
}

func overrideFromRow(row *ent.SourceThroughputPolicy) Override {
	override := Override{DownloadConcurrency: row.DownloadConcurrency}
	if row.ImageRequestDelayMs != nil {
		delay := time.Duration(*row.ImageRequestDelayMs) * time.Millisecond
		override.ImageRequestDelay = &delay
	}
	return override
}

func applyPatch(override *Override, patch Patch) {
	switch patch.DownloadConcurrency.Operation {
	case PatchSet:
		value := patch.DownloadConcurrency.Value
		override.DownloadConcurrency = &value
	case PatchClear:
		override.DownloadConcurrency = nil
	}

	switch patch.ImageRequestDelay.Operation {
	case PatchSet:
		value := patch.ImageRequestDelay.Value
		override.ImageRequestDelay = &value
	case PatchClear:
		override.ImageRequestDelay = nil
	}
}

func durationMillis(delay *time.Duration) *int64 {
	if delay == nil {
		return nil
	}
	ms := delay.Milliseconds()
	return &ms
}
