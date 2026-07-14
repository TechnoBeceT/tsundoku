// Package notify is the decoupled new-readable-chapter notifier. It runs as one
// best-effort pass at the END of every download cycle (job.Runner.RunDownloadCycle),
// finds chapters that became readable since a persisted watermark, applies a
// per-series "caught-up arming" guard so a fresh adopt/import's backlog never
// storms the owner, groups the survivors by series, and dispatches ONE payload
// over BOTH channels — the chapter.new SSE event (open clients) and Web Push
// (closed clients, via the injected Pusher).
//
// # Trigger discipline
//
// The pass keys STRICTLY on Chapter.first_downloaded_at (write-once, set on the
// first successful download and NEVER rewritten). It deliberately ignores
// download_date, which a Library-Convergence upgrade rewrites — keying on that
// would ping the owner every time an old chapter was silently re-fetched from a
// better source.
//
// # Best-effort
//
// A soft failure (DB read/write hiccup, push failure) is logged and swallowed;
// NotifyNewChapters returns nil so it can never break the download cycle. The
// watermark and per-series notify_armed flag are monotonic — the pass never
// double-fires the same chapter and never replays a backlog.
package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entsettings "github.com/technobecet/tsundoku/internal/ent/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// watermarkKey is the Settings row that stores the RFC3339 timestamp of the
// most-recent chapter first_downloaded_at the notifier has already accounted
// for. It lives under the internal.* namespace so it is invisible to the
// settings API allowlist (it is read/written by direct ent, never through
// settings.Service).
const watermarkKey = "internal.notify.last_notified_at"

// Pusher dispatches a rendered notification to every Web Push subscription. It
// is best-effort (returns nothing): a push failure must never affect the notify
// pass. Defined here (not imported from internal/push) so notify has no
// dependency on the push transport — internal/push.Sender satisfies it.
type Pusher interface {
	Push(ctx context.Context, payload NewChapterNotification)
}

// Broadcaster fans an SSE event out to open clients. Satisfied by *sse.Hub.
type Broadcaster interface {
	Broadcast(sse.Event)
}

// Toggle reports whether new-chapter notifications are currently enabled. It is
// the notifications.enabled runtime tunable, satisfied by *settings.Service, so
// a change hot-reloads on the next cycle.
type Toggle interface {
	NotificationsEnabled(ctx context.Context) bool
}

// Service runs the notifier pass. Construct with NewService.
type Service struct {
	client *ent.Client
	hub    Broadcaster
	pusher Pusher
	toggle Toggle
}

// NewService builds a notifier over the Ent client, the SSE hub (open-client
// broadcast), the Web Push sender (closed-client push), and the notifications
// toggle. All collaborators are interfaces so the pass is fully unit-testable
// with fakes.
func NewService(client *ent.Client, hub Broadcaster, pusher Pusher, toggle Toggle) *Service {
	return &Service{client: client, hub: hub, pusher: pusher, toggle: toggle}
}

// NotifyNewChapters runs one notifier pass. It is best-effort: every soft
// failure is logged and swallowed, and it always returns nil so the download
// cycle is never broken. See the package doc for the trigger + arming discipline.
func (s *Service) NotifyNewChapters(ctx context.Context) error {
	// Toggle off: skip entirely WITHOUT touching the watermark, so re-enabling
	// later does not storm the owner with everything downloaded while it was off.
	if !s.toggle.NotificationsEnabled(ctx) {
		return nil
	}

	watermark, err := s.readWatermark(ctx)
	if err != nil {
		slog.WarnContext(ctx, "notify: read watermark failed, skipping cycle", "err", err)
		return nil
	}

	// One index-friendly query: chapters newly readable since the watermark,
	// whose series is monitored + non-completed, with the series eager-loaded.
	chapters, err := s.client.Chapter.Query().
		Where(
			entchapter.FirstDownloadedAtGT(watermark),
			entchapter.HasSeriesWith(
				entseries.MonitoredEQ(true),
				entseries.CompletedEQ(false),
			),
		).
		WithSeries().
		All(ctx)
	if err != nil {
		slog.WarnContext(ctx, "notify: query new chapters failed, skipping cycle", "err", err)
		return nil
	}

	if len(chapters) == 0 {
		return nil // nothing new; watermark stays put (nothing to advance past)
	}

	maxSeen, bySeries := s.partition(chapters, watermark)
	groups := s.armAndCollect(ctx, bySeries)

	if len(groups) > 0 {
		s.dispatch(ctx, groups)
	}

	// ALWAYS advance the watermark past everything seen this cycle — including
	// suppressed/just-armed series — so a backlog never re-surfaces on a later pass.
	if err := s.writeWatermark(ctx, maxSeen); err != nil {
		slog.WarnContext(ctx, "notify: persist watermark failed", "err", err)
	}
	return nil
}

// seriesGroup accumulates one series' candidate chapters during a pass.
type seriesGroup struct {
	series   *ent.Series
	chapters []*ent.Chapter
}

// partition buckets the candidate chapters by series and tracks the maximum
// first_downloaded_at seen (the next watermark). Chapters whose series edge is
// missing (structurally impossible given the query, but defended) are skipped.
func (s *Service) partition(chapters []*ent.Chapter, watermark time.Time) (maxSeen time.Time, bySeries map[uuid.UUID]*seriesGroup) {
	maxSeen = watermark
	bySeries = make(map[uuid.UUID]*seriesGroup)
	for _, ch := range chapters {
		if ch.FirstDownloadedAt != nil && ch.FirstDownloadedAt.After(maxSeen) {
			maxSeen = *ch.FirstDownloadedAt
		}
		sr := ch.Edges.Series
		if sr == nil {
			continue
		}
		g, ok := bySeries[sr.ID]
		if !ok {
			g = &seriesGroup{series: sr}
			bySeries[sr.ID] = g
		}
		g.chapters = append(g.chapters, ch)
	}
	return maxSeen, bySeries
}

// armAndCollect applies the caught-up arming guard per series and returns the
// notification groups for the series that actually fire this cycle:
//   - armed series → included (they fire).
//   - unarmed but now caught-up (no wanted/downloading chapters left) → armed
//     for NEXT time, but SUPPRESSED this cycle (kills the fresh-adopt backlog).
//   - unarmed and still not caught-up → neither armed nor fired.
func (s *Service) armAndCollect(ctx context.Context, bySeries map[uuid.UUID]*seriesGroup) []NewChapterGroup {
	groups := make([]NewChapterGroup, 0, len(bySeries))
	for sid, g := range bySeries {
		if g.series.NotifyArmed {
			groups = append(groups, NewChapterGroup{
				SeriesID: sid,
				Title:    g.series.Title,
				Count:    len(g.chapters),
				URL:      "/series/" + sid.String(),
			})
			continue
		}
		if s.caughtUp(ctx, sid) {
			// Arm for next time; suppress this (arming) cycle's backlog tail.
			if err := s.client.Series.UpdateOneID(sid).SetNotifyArmed(true).Exec(ctx); err != nil {
				slog.WarnContext(ctx, "notify: arm series failed", "series", sid, "err", err)
			}
		}
	}
	return groups
}

// caughtUp reports whether a series has NO chapters still in flight (wanted or
// downloading) — i.e. its backlog has drained. A read error fails closed
// (treated as not caught-up) so a transient DB hiccup never arms a series early.
func (s *Service) caughtUp(ctx context.Context, sid uuid.UUID) bool {
	n, err := s.client.Chapter.Query().
		Where(
			entchapter.SeriesIDEQ(sid),
			entchapter.StateIn(entchapter.StateWanted, entchapter.StateDownloading),
		).
		Count(ctx)
	if err != nil {
		slog.WarnContext(ctx, "notify: caught-up count failed", "series", sid, "err", err)
		return false
	}
	return n == 0
}

// dispatch renders the surviving groups and sends the single payload over both
// channels: the chapter.new SSE event and Web Push.
func (s *Service) dispatch(ctx context.Context, groups []NewChapterGroup) {
	total := 0
	for _, g := range groups {
		total += g.Count
	}
	title, body, digest := render(groups)
	payload := NewChapterNotification{
		Groups: groups,
		Total:  total,
		Digest: digest,
		Title:  title,
		Body:   body,
	}
	s.hub.Broadcast(sse.Event{Type: "chapter.new", Data: payload})
	s.pusher.Push(ctx, payload)
}

// BackfillArm arms every existing series once (idempotent) and seeds the
// watermark to now if it is unset. Run once at boot so a caught-up library does
// not re-announce its entire back-catalogue the first time the notifier runs,
// yet still fires for the next genuinely-new chapter.
func (s *Service) BackfillArm(ctx context.Context) error {
	if _, err := s.client.Series.Update().
		Where(entseries.NotifyArmedEQ(false)).
		SetNotifyArmed(true).
		Save(ctx); err != nil {
		return err
	}
	if _, err := s.client.Settings.Query().Where(entsettings.KeyEQ(watermarkKey)).Only(ctx); ent.IsNotFound(err) {
		return s.writeWatermark(ctx, time.Now())
	} else if err != nil {
		return err
	}
	return nil
}

// readWatermark reads the persisted last-notified timestamp. When the row is
// absent (fresh deploy) it seeds it to now and returns now, so the ~thousands of
// already-readable chapters in an existing library never all fire at once.
func (s *Service) readWatermark(ctx context.Context) (time.Time, error) {
	row, err := s.client.Settings.Query().Where(entsettings.KeyEQ(watermarkKey)).Only(ctx)
	if ent.IsNotFound(err) {
		now := time.Now()
		if wErr := s.writeWatermark(ctx, now); wErr != nil {
			return time.Time{}, wErr
		}
		return now, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339Nano, row.Value)
	if err != nil {
		// Corrupt value (never written by us): re-seed to now, fail forward.
		now := time.Now()
		if wErr := s.writeWatermark(ctx, now); wErr != nil {
			return time.Time{}, wErr
		}
		return now, nil
	}
	return t, nil
}

// writeWatermark upserts the last-notified timestamp (RFC3339Nano) via direct
// ent — NEVER through settings.Service, which only knows the public allowlist.
func (s *Service) writeWatermark(ctx context.Context, t time.Time) error {
	value := t.UTC().Format(time.RFC3339Nano)
	existing, err := s.client.Settings.Query().Where(entsettings.KeyEQ(watermarkKey)).Only(ctx)
	if ent.IsNotFound(err) {
		return s.client.Settings.Create().SetKey(watermarkKey).SetValue(value).Exec(ctx)
	}
	if err != nil {
		return err
	}
	return s.client.Settings.UpdateOneID(existing.ID).SetValue(value).Exec(ctx)
}
