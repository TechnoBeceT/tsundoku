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

// backfillDoneKey is the one-time marker that records BackfillArm has already
// run. Once set, later boots NEVER re-run the arm-everything backfill — re-arming
// on a routine restart would defeat the adopt-storm suppression for a series
// still draining its backlog at restart time.
const backfillDoneKey = "internal.notify.backfill_done"

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
	// Toggle off: skip entirely WITHOUT advancing the watermark. Nothing is lost —
	// when the owner re-enables, every chapter downloaded during the off window is
	// still > the (unadvanced) watermark, so an armed series' accumulated new
	// chapters surface then as ONE (digest-collapsed) notification rather than
	// being silently swallowed. This is deliberate: turning notifications off must
	// not lose the news, only defer it into a single catch-up on re-enable.
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
	groups, toArm := s.plan(ctx, bySeries)

	// Persist the just-armed series AND the advanced watermark in ONE transaction
	// BEFORE dispatching. Atomicity is load-bearing: if these were separate writes
	// and the watermark write failed (or the process died between them), a
	// just-armed series' suppressed backlog would be re-selected next pass and
	// storm the owner. On a persist failure we advance nothing and dispatch
	// nothing, so the pass retries cleanly next cycle. A dispatch AFTER the commit
	// can at worst LOSE a notification (best-effort) — it can never double-fire.
	if err := s.persist(ctx, toArm, maxSeen); err != nil {
		slog.WarnContext(ctx, "notify: persist arming+watermark failed, skipping dispatch", "err", err)
		return nil
	}

	if len(groups) > 0 {
		s.dispatch(ctx, groups)
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

// plan applies the caught-up arming guard per series and DECIDES (without
// writing) which series fire this cycle and which are to be armed:
//   - armed series → included in groups (they fire).
//   - unarmed but now caught-up (no wanted/downloading chapters left) → returned
//     in toArm so persist arms them for NEXT time, but SUPPRESSED this cycle
//     (kills the fresh-adopt backlog).
//   - unarmed and still not caught-up → neither armed nor fired.
//
// The writes are deferred to persist so the arming + watermark advance land in
// one transaction (never one without the other).
func (s *Service) plan(ctx context.Context, bySeries map[uuid.UUID]*seriesGroup) (groups []NewChapterGroup, toArm []uuid.UUID) {
	groups = make([]NewChapterGroup, 0, len(bySeries))
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
			toArm = append(toArm, sid)
		}
	}
	return groups, toArm
}

// persist commits the arming writes AND the advanced watermark in a SINGLE
// transaction, so the two can never diverge (see NotifyNewChapters for why the
// atomicity is load-bearing against the adopt-storm). Any failure rolls the whole
// thing back — neither the arm flags nor the watermark move.
func (s *Service) persist(ctx context.Context, toArm []uuid.UUID, watermark time.Time) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	for _, sid := range toArm {
		if uErr := tx.Series.UpdateOneID(sid).SetNotifyArmed(true).Exec(ctx); uErr != nil {
			_ = tx.Rollback()
			return uErr
		}
	}
	if wErr := upsertSettingTx(ctx, tx, watermarkKey, watermark.UTC().Format(time.RFC3339Nano)); wErr != nil {
		_ = tx.Rollback()
		return wErr
	}
	return tx.Commit()
}

// caughtUp reports whether a series has NO chapters still in flight (wanted or
// downloading) — i.e. its backlog has drained. A read error fails closed
// (treated as not caught-up) so a transient DB hiccup never arms a series early.
//
// KNOWN LIMITATION (documented, deferred — owner's-call item from review): a
// chapter stuck permanently `wanted` because NO provider feed carries its
// chapter_key (a sourceless gap) keeps a post-launch series from ever becoming
// caught up, so it never arms and never notifies. Fixing it means ignoring
// wanted chapters with no live candidate, which requires the per-chapter
// ProviderChapter/candidate machinery (internal/chapter) and its own edge cases;
// deferred here to keep the notifier's dependency surface minimal. The common
// case — every wanted chapter has a source and eventually downloads — arms
// correctly. Revisit if sourceless-gap series prove common in practice.
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

// BackfillArm arms every existing series ONCE, EVER, and seeds the watermark to
// now if it is unset. It is guarded by a persisted one-time marker
// (backfillDoneKey): after the first successful run, every later boot is a no-op.
//
// The one-time guard is load-bearing (not just an optimisation): running it on
// every boot would arm a series that was freshly adopted since the last boot and
// is STILL draining its backlog, so its remaining backlog would storm the owner —
// the exact adopt-storm the caught-up arming guard exists to suppress. Existing
// series present at first-ever boot are a caught-up library, so arming them once
// is correct (they never re-announce their back-catalogue); series adopted later
// must go through the normal caught-up-then-arm path, never the backfill.
func (s *Service) BackfillArm(ctx context.Context) error {
	done, err := s.backfillDone(ctx)
	if err != nil {
		return err
	}
	if done {
		return nil // already run on a prior boot; never re-arm
	}

	if _, uErr := s.client.Series.Update().
		Where(entseries.NotifyArmedEQ(false)).
		SetNotifyArmed(true).
		Save(ctx); uErr != nil {
		return uErr
	}
	// Seed the watermark to now if unset (fresh deploy) so an existing library's
	// thousands of already-readable chapters never all fire at once.
	if _, qErr := s.client.Settings.Query().Where(entsettings.KeyEQ(watermarkKey)).Only(ctx); ent.IsNotFound(qErr) {
		if wErr := s.writeWatermark(ctx, time.Now()); wErr != nil {
			return wErr
		}
	} else if qErr != nil {
		return qErr
	}
	// Record that the backfill has run so later boots skip it.
	return s.upsertSetting(ctx, backfillDoneKey, "true")
}

// backfillDone reports whether BackfillArm has already run on a prior boot.
func (s *Service) backfillDone(ctx context.Context) (bool, error) {
	row, err := s.client.Settings.Query().Where(entsettings.KeyEQ(backfillDoneKey)).Only(ctx)
	if ent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.Value == "true", nil
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
	return s.upsertSetting(ctx, watermarkKey, t.UTC().Format(time.RFC3339Nano))
}

// upsertSetting writes an internal.* Settings key=value via direct ent (create
// the first time, update thereafter). The key column is unique, so the
// find-then-write pattern is used.
func (s *Service) upsertSetting(ctx context.Context, key, value string) error {
	existing, err := s.client.Settings.Query().Where(entsettings.KeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		return s.client.Settings.Create().SetKey(key).SetValue(value).Exec(ctx)
	}
	if err != nil {
		return err
	}
	return s.client.Settings.UpdateOneID(existing.ID).SetValue(value).Exec(ctx)
}

// upsertSettingTx is upsertSetting inside an open transaction (used by persist so
// the watermark advance shares one commit with the arming writes).
func upsertSettingTx(ctx context.Context, tx *ent.Tx, key, value string) error {
	existing, err := tx.Settings.Query().Where(entsettings.KeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		return tx.Settings.Create().SetKey(key).SetValue(value).Exec(ctx)
	}
	if err != nil {
		return err
	}
	return tx.Settings.UpdateOneID(existing.ID).SetValue(value).Exec(ctx)
}
