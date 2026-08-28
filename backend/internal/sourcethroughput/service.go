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
	return ApplyDefaults(s.Defaults(ctx), stored), nil
}

// Defaults returns the current fully inherited throughput policy. Runtime
// settings are read at use time so callers observe hot-reloaded global values.
func (s *Service) Defaults(ctx context.Context) Effective {
	return Effective{
		DownloadConcurrency: s.defaults.DownloadConcurrency(ctx),
		ImageRequestDelay:   s.defaults.ImageRequestDelay(ctx),
	}
}

// ApplyDefaults overlays independently optional source values on one captured
// global policy. It lets batched callers resolve a snapshot without re-reading
// the runtime settings for every source.
func ApplyDefaults(defaults Effective, stored Override) Effective {
	effective := defaults
	if stored.DownloadConcurrency != nil {
		effective.DownloadConcurrency = *stored.DownloadConcurrency
	}
	if stored.ImageRequestDelay != nil {
		effective.ImageRequestDelay = *stored.ImageRequestDelay
	}
	return effective
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

	if patch.DownloadConcurrency.Operation == PatchKeep && patch.ImageRequestDelay.Operation == PatchKeep {
		return s.loadOverride(ctx, sourceID)
	}

	// A set-bearing patch is one atomic upsert. Its conflict clause changes only
	// fields named by the patch, so disjoint callers merge and concurrent first
	// writes converge on the unique source_id row without a constraint race.
	if patch.DownloadConcurrency.Operation == PatchSet || patch.ImageRequestDelay.Operation == PatchSet {
		now := time.Now()
		create := s.client.SourceThroughputPolicy.Create().
			SetSourceID(sourceID).
			SetUpdatedAt(now)
		if patch.DownloadConcurrency.Operation == PatchSet {
			create.SetDownloadConcurrency(patch.DownloadConcurrency.Value)
		}
		if patch.ImageRequestDelay.Operation == PatchSet {
			create.SetImageRequestDelayMs(patch.ImageRequestDelay.Value.Milliseconds())
		}

		err := create.
			OnConflictColumns(entpolicy.FieldSourceID).
			Update(func(update *ent.SourceThroughputPolicyUpsert) {
				applyUpsertPatch(update, patch)
				update.SetUpdatedAt(now)
			}).
			Exec(ctx)
		if err != nil {
			return Override{}, fmt.Errorf("sourcethroughput.Update: upsert source %d: %w", sourceID, err)
		}
	} else {
		update := s.client.SourceThroughputPolicy.Update().Where(entpolicy.SourceID(sourceID))
		applyClearPatch(update, patch)
		if _, err := update.Save(ctx); err != nil {
			return Override{}, fmt.Errorf("sourcethroughput.Update: clear source %d: %w", sourceID, err)
		}
	}

	// Delete only if the row is still empty at statement execution time. A
	// concurrent setter makes this predicate false and keeps its override.
	if _, err := s.client.SourceThroughputPolicy.Delete().
		Where(
			entpolicy.SourceID(sourceID),
			entpolicy.DownloadConcurrencyIsNil(),
			entpolicy.ImageRequestDelayMsIsNil(),
		).
		Exec(ctx); err != nil {
		return Override{}, fmt.Errorf("sourcethroughput.Update: delete empty source %d: %w", sourceID, err)
	}

	return s.loadOverride(ctx, sourceID)
}

func overrideFromRow(row *ent.SourceThroughputPolicy) Override {
	override := Override{DownloadConcurrency: row.DownloadConcurrency}
	if row.ImageRequestDelayMs != nil {
		delay := time.Duration(*row.ImageRequestDelayMs) * time.Millisecond
		override.ImageRequestDelay = &delay
	}
	return override
}

func applyUpsertPatch(update *ent.SourceThroughputPolicyUpsert, patch Patch) {
	switch patch.DownloadConcurrency.Operation {
	case PatchSet:
		update.SetDownloadConcurrency(patch.DownloadConcurrency.Value)
	case PatchClear:
		update.ClearDownloadConcurrency()
	}

	switch patch.ImageRequestDelay.Operation {
	case PatchSet:
		update.SetImageRequestDelayMs(patch.ImageRequestDelay.Value.Milliseconds())
	case PatchClear:
		update.ClearImageRequestDelayMs()
	}
}

func applyClearPatch(update *ent.SourceThroughputPolicyUpdate, patch Patch) {
	if patch.DownloadConcurrency.Operation == PatchClear {
		update.ClearDownloadConcurrency()
	}
	if patch.ImageRequestDelay.Operation == PatchClear {
		update.ClearImageRequestDelayMs()
	}
}

func (s *Service) loadOverride(ctx context.Context, sourceID int64) (Override, error) {
	row, err := s.client.SourceThroughputPolicy.Query().
		Where(entpolicy.SourceID(sourceID)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return Override{}, nil
	}
	if err != nil {
		return Override{}, fmt.Errorf("sourcethroughput.Update: query source %d: %w", sourceID, err)
	}
	return overrideFromRow(row), nil
}
