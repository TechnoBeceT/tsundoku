package series

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// defaultStalledThresholdDays is the fallback stalled threshold (days) for a
// Service constructed without WithStalledThreshold — every test and any caller
// that does not wire the settings overlay. Production injects the hot-reloadable
// settings.Service.StalledThresholdDays instead (see WithStalledThreshold),
// mirroring health.stale_grace_days. Kept in lockstep with the config /
// settings default (30).
const defaultStalledThresholdDays = 30

// WithStalledThreshold attaches a use-time resolver for the stalled threshold
// (days) and returns the service, so a Service built with the fixed NewService
// can be upgraded to read settings.Service.StalledThresholdDays at call time
// (hot reload) without changing the 100+ existing NewService call sites (mirrors
// WithCoverFetcher / the staleGrace resolver). A nil resolver is ignored — the
// default constant stays in force.
func (s *Service) WithStalledThreshold(resolve func(ctx context.Context) int) *Service {
	if resolve != nil {
		s.stalledThreshold = resolve
	}
	return s
}

// satKey addresses one (provider, chapter_key) pair — the lookup used to prefer
// the SATISFYING provider's own upload date for a chapter's effective release
// date over the best-provider fallback.
type satKey struct {
	provider uuid.UUID
	key      string
}

// effectiveReleaseDate resolves ONE chapter's release date (the owner-specified
// coalesce): the SATISFYING provider's provider_upload_date for this chapter_key,
// else the highest-importance provider's, else the chapter's download_date (so
// every downloaded chapter always has a date — QCAT-297). Returns nil only for a
// chapter no source dated AND that was never downloaded.
func effectiveReleaseDate(ch *ent.Chapter, bestUpload map[string]*time.Time, satisfierUpload map[satKey]*time.Time) *time.Time {
	if ch.SatisfiedByProviderID != nil {
		if d := satisfierUpload[satKey{provider: *ch.SatisfiedByProviderID, key: ch.ChapterKey}]; d != nil {
			return d
		}
	}
	if d := bestUpload[ch.ChapterKey]; d != nil {
		return d
	}
	return ch.DownloadDate
}

// chapterReleaseDates builds a chapterID → effective release date map for a
// fully-loaded series (row.Edges.Chapters + row.Edges.Providers, each with
// ProviderChapters, must be eager-loaded — GetSeries loads both). It picks each
// key's best-provider upload date (highest importance carrying a non-nil date —
// mirrors ChapterTitles' best-provider rule) plus a per-(provider,key) index so a
// chapter's own satisfying source wins. Pure in-memory, no N+1: one pass over the
// already-loaded feed rows.
func chapterReleaseDates(row *ent.Series) map[uuid.UUID]*time.Time {
	bestUpload := make(map[string]*time.Time)
	bestImportance := make(map[string]int)
	satisfierUpload := make(map[satKey]*time.Time)
	for _, p := range row.Edges.Providers {
		for _, pc := range p.Edges.ProviderChapters {
			if pc.ProviderUploadDate == nil {
				continue
			}
			satisfierUpload[satKey{provider: p.ID, key: pc.ChapterKey}] = pc.ProviderUploadDate
			if cur, seen := bestImportance[pc.ChapterKey]; !seen || p.Importance > cur {
				bestUpload[pc.ChapterKey] = pc.ProviderUploadDate
				bestImportance[pc.ChapterKey] = p.Importance
			}
		}
	}

	out := make(map[uuid.UUID]*time.Time, len(row.Edges.Chapters))
	for _, ch := range row.Edges.Chapters {
		out[ch.ID] = effectiveReleaseDate(ch, bestUpload, satisfierUpload)
	}
	return out
}

// coalesceTime returns primary when non-nil, else fallback — the series-level
// "upload date, else download date" fold behind latestChapterAt.
func coalesceTime(primary, fallback *time.Time) *time.Time {
	if primary != nil {
		return primary
	}
	return fallback
}

// providersUploadMax returns the newest provider_upload_date across ALL of the
// loaded providers' feeds (nil when none carries one). The series-level upload
// component of latestChapterAt — the newest chapter ANY source has published.
func providersUploadMax(providers []*ent.SeriesProvider) *time.Time {
	var newest *time.Time
	for _, p := range providers {
		for _, pc := range p.Edges.ProviderChapters {
			newest = laterTime(newest, pc.ProviderUploadDate)
		}
	}
	return newest
}

// chaptersDownloadMax returns the newest download_date across the loaded
// chapters, excluding superseded/ignored ones (mirrors the count rollups). The
// series-level download fallback of latestChapterAt — used only when no source
// dated any chapter (a fully legacy series), so a convergence upgrade's rewritten
// download_date can never inflate the value while an upload date exists.
func chaptersDownloadMax(chapters []*ent.Chapter) *time.Time {
	var newest *time.Time
	for _, ch := range chapters {
		if ch.State == entchapter.StateSuperseded || ch.State == entchapter.StateIgnored {
			continue
		}
		newest = laterTime(newest, ch.DownloadDate)
	}
	return newest
}

// stalledSeries reports whether a series is STALLED: its newest chapter released
// longer than thresholdDays ago AND the owner is still waiting on it — monitored
// AND not completed (the same predicate the refresh sweep uses; owner-refined
// QCAT-297: a paused/finished series has nothing to wait for). A series with no
// dated chapter at all (latest == nil) is never stalled — there is no prior
// release to have stalled from. Purely informational: nothing acts on it.
func stalledSeries(latest *time.Time, monitored, completed bool, now time.Time, thresholdDays int) bool {
	if latest == nil || !monitored || completed {
		return false
	}
	return now.Sub(*latest) > time.Duration(thresholdDays)*24*time.Hour
}
