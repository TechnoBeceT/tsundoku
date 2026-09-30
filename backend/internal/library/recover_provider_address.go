package library

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// ErrAddressRecoverySkipped means search provided insufficient evidence to
// safely change a provider address. The existing provider and feed are kept.
var ErrAddressRecoverySkipped = errors.New("provider address recovery skipped")

type sourceSearcher interface {
	Search(context.Context, int64, string, int) (sourceengine.SearchResult, error)
}

// RecoverProviderAddress searches the provider's own source after its stored
// address has stopped working. It changes the address only when one exact-title
// result has substantial overlap with the existing chapter feed. Other failures
// remain ordinary refresh errors; none remove provider, chapter, or file rows.
func (s *Service) RecoverProviderAddress(ctx context.Context, providerID uuid.UUID, pace func(context.Context)) error {
	provider, err := s.db.SeriesProvider.Query().Where(seriesprovider.IDEQ(providerID)).WithSeries().Only(ctx)
	if err != nil {
		return fmt.Errorf("load provider: %w", err)
	}
	sourceID, err := parseSourceID(provider.Provider)
	if err != nil {
		return ErrAddressRecoverySkipped
	}
	searcher, ok := s.sources.(sourceSearcher)
	if !ok {
		return ErrAddressRecoverySkipped
	}
	title := provider.Title
	if strings.TrimSpace(title) == "" {
		title = provider.Edges.Series.Title
	}
	paceRecovery(ctx, pace)
	found, err := searcher.Search(ctx, sourceID, title, 1)
	if err != nil {
		return fmt.Errorf("search provider source: %w", err)
	}
	if found.HasNextPage {
		return ErrAddressRecoverySkipped
	}
	oldKeys, err := s.db.ProviderChapter.Query().Where(providerchapter.SeriesProviderID(providerID)).Select(providerchapter.FieldChapterKey).Strings(ctx)
	if err != nil {
		return fmt.Errorf("load provider feed: %w", err)
	}
	keySet := make(map[string]struct{}, len(oldKeys))
	for _, key := range oldKeys {
		keySet[key] = struct{}{}
	}
	resolved, err := s.resolveRecoveryCandidate(ctx, provider, sourceID, title, found.Manga, keySet, pace)
	if err != nil {
		return err
	}
	return s.applyRecoveredProviderAddress(ctx, provider, resolved)
}

func (s *Service) resolveRecoveryCandidate(ctx context.Context, provider *ent.SeriesProvider, sourceID int64, title string, results []sourceengine.MangaEntry, oldKeys map[string]struct{}, pace func(context.Context)) (ingest.ProviderReconcileInput, error) {
	for _, candidate := range results {
		if !strings.EqualFold(strings.TrimSpace(candidate.Title), strings.TrimSpace(title)) {
			continue
		}
		if candidate.URL == "" || candidate.URL == provider.URL || !candidate.AddressMode.IsValid() {
			return ingest.ProviderReconcileInput{}, ErrAddressRecoverySkipped
		}
		ref := sourceengine.ProviderRef{SourceID: sourceID, URL: candidate.URL, WebURL: candidate.RealURL, AddressMode: candidate.AddressMode}
		resolved, resolveErr := s.ingest.ResolveProviderPaced(ctx, ref, title, provider.ProviderName, pace)
		if resolveErr != nil {
			return ingest.ProviderReconcileInput{}, fmt.Errorf("resolve recovery candidate: %w", resolveErr)
		}
		if !strings.EqualFold(strings.TrimSpace(resolved.Title), strings.TrimSpace(title)) {
			return ingest.ProviderReconcileInput{}, ErrAddressRecoverySkipped
		}
		if !safeRecoveryCandidate(title, provider.URL, results, candidate, oldKeys, resolved.Chapters) {
			return ingest.ProviderReconcileInput{}, ErrAddressRecoverySkipped
		}
		return resolved, nil
	}
	return ingest.ProviderReconcileInput{}, ErrAddressRecoverySkipped
}

func (s *Service) applyRecoveredProviderAddress(ctx context.Context, provider *ent.SeriesProvider, resolved ingest.ProviderReconcileInput) error {
	if !s.acquireMerge(provider.SeriesID) {
		return ErrMergeInFlight
	}
	defer s.releaseMerge(provider.SeriesID)
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin address recovery: %w", err)
	}
	current, err := tx.SeriesProvider.Get(ctx, provider.ID)
	if err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if current.URL != provider.URL || current.WebURL != provider.WebURL || current.AddressMode != provider.AddressMode {
		return errors.Join(ErrAddressRecoverySkipped, tx.Rollback())
	}
	if _, err = s.ingest.ReconcileProvider(ctx, tx, provider.ID, resolved, ingest.ReplaceProviderAddress); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit address recovery: %w", err)
	}
	return nil
}

func safeRecoveryCandidate(title, oldURL string, results []sourceengine.MangaEntry, selected sourceengine.MangaEntry, oldKeys map[string]struct{}, chapters []sourceengine.Chapter) bool {
	if selected.URL == "" || selected.URL == oldURL || !selected.AddressMode.IsValid() {
		return false
	}
	exact := exactTitleCount(title, results)
	if exact != 1 || !strings.EqualFold(strings.TrimSpace(selected.Title), strings.TrimSpace(title)) {
		return false
	}
	if len(oldKeys) < 3 || len(chapters) < 3 {
		return false
	}
	newKeys := recoveryChapterKeys(chapters)
	overlap, minimum := recoveryOverlap(oldKeys, newKeys)
	return overlap >= 3 && overlap*4 >= minimum*3
}

func recoveryOverlap(oldKeys, newKeys map[string]struct{}) (int, int) {
	overlap := 0
	for key := range newKeys {
		if _, ok := oldKeys[key]; ok {
			overlap++
		}
	}
	minimum := len(oldKeys)
	if len(newKeys) < minimum {
		minimum = len(newKeys)
	}
	return overlap, minimum
}

func paceRecovery(ctx context.Context, pace func(context.Context)) {
	if pace != nil {
		pace(ctx)
	}
}

func exactTitleCount(title string, results []sourceengine.MangaEntry) int {
	count := 0
	for _, result := range results {
		if strings.EqualFold(strings.TrimSpace(result.Title), strings.TrimSpace(title)) {
			count++
		}
	}
	return count
}

func recoveryChapterKeys(chapters []sourceengine.Chapter) map[string]struct{} {
	keys := make(map[string]struct{}, len(chapters))
	for _, ch := range chapters {
		var number *float64
		if ch.Number >= 0 {
			n := ch.Number
			number = &n
		}
		keys[chapter.NormalizeChapterKey(number, ch.Name)] = struct{}{}
	}
	return keys
}
