package download

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
)

// ProviderChapterCacheInvalidator removes the disposable staging directory
// paired with a ProviderChapter's cached page links. A blank staging root is a
// no-op for deployments that do not configure disk staging.
type ProviderChapterCacheInvalidator struct {
	stagingRoot string
}

// NewProviderChapterCacheInvalidator returns an invalidator rooted at the same
// staging directory supplied to the page fetcher and download dispatcher.
func NewProviderChapterCacheInvalidator(stagingRoot string) *ProviderChapterCacheInvalidator {
	return &ProviderChapterCacheInvalidator{stagingRoot: stagingRoot}
}

// BeginProviderChapterCacheInvalidation starts a recoverable staging-cache
// journal. Invalidated directories are renamed into a private quarantine until
// the owning database transaction commits or rolls back.
func (i *ProviderChapterCacheInvalidator) BeginProviderChapterCacheInvalidation() chapter.ProviderChapterCacheInvalidation {
	if i == nil || i.stagingRoot == "" {
		return &providerChapterCacheInvalidation{}
	}
	return &providerChapterCacheInvalidation{
		stagingRoot: i.stagingRoot,
		quarantine:  filepath.Join(i.stagingRoot, ".provider-cache-invalidations", uuid.NewString()),
		moved:       make(map[uuid.UUID]bool),
	}
}

// InvalidateProviderChapterCache performs one immediately committed cache
// invalidation. Reconcile code uses BeginProviderChapterCacheInvalidation so a
// surrounding database rollback can restore the staged bytes.
func (i *ProviderChapterCacheInvalidator) InvalidateProviderChapterCache(ctx context.Context, providerChapterID uuid.UUID) error {
	tx := i.BeginProviderChapterCacheInvalidation()
	if err := tx.InvalidateProviderChapterCache(ctx, providerChapterID); err != nil {
		_ = tx.Rollback()
		return err
	}
	tx.Commit()
	return nil
}

type providerChapterCacheInvalidation struct {
	stagingRoot string
	quarantine  string
	moved       map[uuid.UUID]bool
}

func (tx *providerChapterCacheInvalidation) InvalidateProviderChapterCache(_ context.Context, providerChapterID uuid.UUID) error {
	if tx.stagingRoot == "" || tx.moved[providerChapterID] {
		return nil
	}
	source := filepath.Join(tx.stagingRoot, providerChapterID.String())
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			tx.moved[providerChapterID] = false
			return nil
		}
		return err
	}
	if err := os.MkdirAll(tx.quarantine, 0o750); err != nil {
		return err
	}
	if err := os.Rename(source, filepath.Join(tx.quarantine, providerChapterID.String())); err != nil {
		return err
	}
	tx.moved[providerChapterID] = true
	return nil
}

func (tx *providerChapterCacheInvalidation) Commit() {
	if tx.quarantine == "" {
		return
	}
	if err := os.RemoveAll(tx.quarantine); err != nil {
		slog.Warn("download: could not remove committed provider cache quarantine", "quarantine", tx.quarantine, "err", err)
	}
}

func (tx *providerChapterCacheInvalidation) Rollback() error {
	if tx.quarantine == "" {
		return nil
	}
	var rollbackErr error
	for providerChapterID, moved := range tx.moved {
		if !moved {
			continue
		}
		source := filepath.Join(tx.stagingRoot, providerChapterID.String())
		backup := filepath.Join(tx.quarantine, providerChapterID.String())
		if err := os.RemoveAll(source); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove replacement staging %q: %w", source, err))
			continue
		}
		if err := os.Rename(backup, source); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore quarantined staging %q: %w", source, err))
		}
	}
	// Keep the quarantine intact when any restore failed: deleting it here would
	// destroy the only remaining copy of those staged bytes.
	if rollbackErr == nil {
		if err := os.RemoveAll(tx.quarantine); err != nil {
			rollbackErr = fmt.Errorf("remove cache quarantine %q: %w", tx.quarantine, err)
		}
	}
	return rollbackErr
}
