package sourcepurge

import (
	"context"
	"fmt"
	"strconv"

	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
)

// extensionSourceIDs reads the durable pkgName→source-ids map from the
// enginetopo HarvestedExtension store. It is the DB record, NOT a live engine
// call: a purge frequently runs when the extension is already uninstalled (so the
// engine no longer reports its sources), and only Tsundoku's own durable capture
// still knows which sources it provided. An absent row (never harvested / already
// pruned) yields an empty slice — nothing to purge, not an error.
func (s *Service) extensionSourceIDs(ctx context.Context, pkgName string) ([]int64, error) {
	row, err := s.db.HarvestedExtension.Query().
		Where(entharvestedextension.PkgName(pkgName)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sourcepurge: load harvested extension %q: %w", pkgName, err)
	}
	return row.SourceIds, nil
}

// PurgeExtension resolves the extension's source ids from the durable store and
// purges each — the extension-level cascade (also driven automatically by the
// uninstall write-through). Fault-isolated: a single source's purge failure is
// recorded in the summary's Errors and never aborts the remaining sources
// (mirrors the enginetopo reconcile per-item isolation). Only a failure to READ
// the durable store returns a top-level error.
func (s *Service) PurgeExtension(ctx context.Context, pkgName string) (ExtensionSummary, error) {
	ids, err := s.extensionSourceIDs(ctx, pkgName)
	if err != nil {
		return ExtensionSummary{PkgName: pkgName}, err
	}
	out := ExtensionSummary{PkgName: pkgName}
	for _, id := range ids {
		sourceID := strconv.FormatInt(id, 10)
		sum, pErr := s.PurgeSource(ctx, sourceID, "")
		out.Sources = append(out.Sources, sum)
		out.SeriesAffected += sum.SeriesAffected
		out.ProvidersRemoved += sum.ProvidersRemoved
		out.ChaptersDeleted += sum.ChaptersDeleted
		out.MetricsDeleted += sum.MetricsDeleted
		out.BreakerCleared += sum.BreakerCleared
		if pErr != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("source %s: %v", sourceID, pErr))
		}
	}
	return out, nil
}

// PreviewExtension aggregates the dry-run counts across every source the
// extension provides — the confirm-dialog figures for an extension-level purge.
// A per-source count failure is surfaced (returned) rather than hidden, since the
// preview must not under-report what a purge would touch.
func (s *Service) PreviewExtension(ctx context.Context, pkgName string) (ExtensionPreview, error) {
	ids, err := s.extensionSourceIDs(ctx, pkgName)
	if err != nil {
		return ExtensionPreview{PkgName: pkgName}, err
	}
	out := ExtensionPreview{PkgName: pkgName}
	for _, id := range ids {
		p, pErr := s.PreviewSource(ctx, strconv.FormatInt(id, 10), "")
		if pErr != nil {
			return ExtensionPreview{}, pErr
		}
		out.Sources = append(out.Sources, p)
		out.SeriesAffected += p.SeriesAffected
		out.Providers += p.Providers
		out.ProviderChapters += p.ProviderChapters
		out.ChaptersDeleted += p.ChaptersDeleted
		out.Metrics += p.Metrics
		out.Breaker += p.Breaker
	}
	return out, nil
}
