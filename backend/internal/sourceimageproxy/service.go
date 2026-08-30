package sourceimageproxy

import (
	"context"
	"errors"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	settingssvc "github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

var (
	// ErrSourceNotFound reports that the requested source is absent from the
	// current live engine catalog.
	ErrSourceNotFound = errors.New("source not found")
	// ErrCatalogUnavailable reports that the live installed-source catalog could
	// not be loaded. Updates fail closed and persist nothing in this case.
	ErrCatalogUnavailable = errors.New("source catalog unavailable")
)

// SourceCatalog is the live engine-host source registry used to validate a
// source before any membership or intent write begins.
type SourceCatalog interface {
	Sources(context.Context) ([]sourceengine.Source, error)
}

// UpdateResult is the committed membership and source runtime intent after one
// successful source-scoped update.
type UpdateResult struct {
	Enabled   bool
	SourceIDs []int64
	Intent    sourcetransport.Intent
}

// Service commits image-proxy membership with its source runtime intent.
type Service struct {
	client    *ent.Client
	settings  *settingssvc.Service
	transport *sourcetransport.Service
	catalog   SourceCatalog
}

// NewService constructs the source-scoped image-proxy writer.
func NewService(client *ent.Client, settings *settingssvc.Service, transport *sourcetransport.Service, catalog SourceCatalog) *Service {
	return &Service{client: client, settings: settings, transport: transport, catalog: catalog}
}

// Update validates sourceID against the live catalog, then changes exactly its
// allowlist membership and advances its desired runtime revision atomically.
func (s *Service) Update(ctx context.Context, sourceID int64, enabled bool) (UpdateResult, error) {
	sources, err := s.catalog.Sources(ctx)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("sourceimageproxy.Update: %w: %w", ErrCatalogUnavailable, err)
	}
	found := false
	for _, source := range sources {
		if source.ID == sourceID {
			found = true
			break
		}
	}
	if !found {
		return UpdateResult{}, fmt.Errorf("sourceimageproxy.Update source %d: %w", sourceID, ErrSourceNotFound)
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("sourceimageproxy.Update: begin transaction: %w", err)
	}
	ids, err := s.settings.UpdateImpersonateSourceTx(ctx, tx, sourceID, enabled)
	if err != nil {
		_ = tx.Rollback()
		return UpdateResult{}, fmt.Errorf("sourceimageproxy.Update: %w", err)
	}
	intent, err := s.transport.AdvanceIntentTx(ctx, tx, sourceID)
	if err != nil {
		_ = tx.Rollback()
		return UpdateResult{}, fmt.Errorf("sourceimageproxy.Update: advance source %d intent: %w", sourceID, err)
	}
	if err := tx.Commit(); err != nil {
		return UpdateResult{}, fmt.Errorf("sourceimageproxy.Update: commit source %d: %w", sourceID, err)
	}
	return UpdateResult{Enabled: enabled, SourceIDs: ids, Intent: intent}, nil
}
