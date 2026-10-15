package download

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// SourceScheduler owns transient source windows for one runner. Poll and Step are called
// by one coordinator; only launched chapter goroutines execute chapter work. A window
// has the same 2*concurrency allowance as a former cycle, shared by downloads and
// upgrades. An idle source renews independently on a start-based interval.
// No persistent queue or chapter-state owner is introduced here: admission still
// goes through the exact-generation conditional claim at the engine boundary.
type SourceScheduler struct {
	d               *Dispatcher
	ctx             context.Context
	parent          context.Context
	cancel          context.CancelFunc
	global          *fetchCapacity
	limiter         *providerLimiter
	sources         map[string]*sourceWindow
	inFlight        map[uuid.UUID]bool
	done            chan sourceCompletion
	wake            chan struct{}
	queuedAfter     *ent.Chapter
	policy          sourceConcurrencyPolicy
	queuedDone      bool
	discovering     bool
	ordinaryPhase   bool
	pendingPriority map[string]int64
}

type sourceWindow struct {
	started    time.Time
	remaining  int
	running    bool
	active     int
	seen       map[uuid.UUID]bool
	lastSeries uuid.UUID
	nextSeries uuid.UUID
	turnSeen   bool
}

type sourceWork struct {
	id       uuid.UUID
	seriesID uuid.UUID
	download *resolvedChapter
	priority bool
}

type sourceCompletion struct {
	key      string
	ids      []uuid.UUID
	upgraded int
	err      error
}

// SourceStep reports one bounded discovery pass and completed windows. More
// means another page remains; it is not evidence that any chapter is actionable.
type SourceStep struct {
	Running   bool
	More      bool
	Started   bool
	Completed bool
	Upgraded  int
	NextRunAt time.Time
	WakeAt    time.Time
}

// NewSourceScheduler captures the initial policy before selection and creates
// the shared global/source fetch bounds for all overlapping windows.
func (d *Dispatcher) NewSourceScheduler(ctx context.Context) (*SourceScheduler, error) {
	cycleCtx, err := d.BeginCycle(ctx)
	if err != nil {
		return nil, err
	}
	policy, err := d.concurrencyPolicy(cycleCtx)
	if err != nil {
		return nil, err
	}
	owned, cancel := context.WithCancel(cycleCtx)
	return &SourceScheduler{d: d, ctx: owned, parent: cycleCtx, cancel: cancel,
		global:  newFetchCapacity(d.MaxConcurrentDownloads(ctx)),
		limiter: newPolicyProviderLimiter(policy), policy: policy,
		sources: make(map[string]*sourceWindow), inFlight: make(map[uuid.UUID]bool), done: make(chan sourceCompletion, 128), wake: make(chan struct{}, 1)}, nil
}

// RestartDiscovery observes newly changed owner scheduling inputs from the start
// of the bounded priority ordering. Continuation cursors remain transient.
func (s *SourceScheduler) RestartDiscovery() {
	s.queuedAfter = nil
	s.ordinaryPhase = false
	s.queuedDone = false
	s.discovering = true
	s.pendingPriority = make(map[string]int64)
	for _, window := range s.sources {
		window.turnSeen = false
	}
}

// CompletionSignal wakes the coordinator when a launched chapter frees a slot.
// The notification coalesces; Poll remains the owner of completion accounting.
func (s *SourceScheduler) CompletionSignal() <-chan struct{} { return s.wake }

// RefreshPolicy applies runtime changes to future windows and shared admission
// bounds without replacing permits held by current calls.
func (s *SourceScheduler) RefreshPolicy() error {
	ctx, err := s.d.BeginCycle(s.ctx)
	if err != nil {
		return err
	}
	policy, err := s.d.concurrencyPolicy(ctx)
	if err != nil {
		return err
	}
	s.policy = policy
	s.limiter.setPolicy(policy)
	s.global.setLimit(s.d.MaxConcurrentDownloads(s.ctx))
	for key, window := range s.sources {
		if key != unresolvedTargetKey {
			// Refresh can tighten a recognized alias, but cannot renew its
			// window or forget starts already charged to the shared allowance.
			remaining := max(0, BatchPerSource(policy.ForSource(key, 0))-len(window.seen))
			window.remaining = min(window.remaining, remaining)
		}
	}
	return nil
}

// Step discovers at most one page of downloads and upgrades. interval=0 is a
// single bounded drain with no allowance renewal. Triggers only wake discovery; they cannot reset or overlap a source window.
func (s *SourceScheduler) Step(now time.Time, interval time.Duration) (SourceStep, error) {
	result, err := s.Poll(now, interval)
	if err != nil {
		return result, err
	}
	maxRetries := s.d.retry.MaxRetries(s.ctx)
	disabled, err := s.d.disabledSourceSet(s.ctx)
	if err != nil {
		return result, err
	}
	if !s.discovering {
		s.RestartDiscovery()
	}
	work, sourceIDs, priority, err := s.nextDiscovery(&result, now, maxRetries, disabled)
	if err != nil {
		return result, err
	}
	s.launchDiscovered(&result, work, sourceIDs, priority, now, interval, maxRetries, disabled)
	if !s.discovering && s.pendingPriorityCanLaunch() {
		s.RestartDiscovery()
		result.More = true
	}
	result.Running = false
	result.NextRunAt = time.Time{}
	result.WakeAt = time.Time{}
	s.markStepStatus(&result, now, interval)
	return result, nil
}

func (s *SourceScheduler) nextDiscovery(result *SourceStep, now time.Time, maxRetries int, disabled map[int64]bool) (map[string][]sourceWork, map[string]int64, bool, error) {
	priority := !s.ordinaryPhase
	work, sourceIDs, more, err := s.discover(now, maxRetries, disabled)
	if err != nil {
		return nil, nil, false, err
	}
	if priority && !more && len(work) == 0 {
		// Finish the marked sweep before the empty-page shortcut changes kind.
		// Otherwise a missing turn can survive every exact-page continuation.
		s.clearMissingTurns()
		s.advanceDiscovery(result, false, true)
		priority = false
		work, sourceIDs, more, err = s.discover(now, maxRetries, disabled)
		if err != nil {
			return nil, nil, false, err
		}
	}
	s.advanceDiscovery(result, more, priority)
	return work, sourceIDs, priority, nil
}

func (s *SourceScheduler) advanceDiscovery(result *SourceStep, more, priority bool) {
	result.More = more
	if !more {
		if priority {
			s.ordinaryPhase = true
			s.queuedDone = false
			result.More = true
		} else {
			s.discovering = false
		}
	}
}

func (s *SourceScheduler) launchDiscovered(result *SourceStep, work map[string][]sourceWork, sourceIDs map[string]int64, priority bool, now time.Time, interval time.Duration, maxRetries int, disabled map[int64]bool) {
	for key, items := range work {
		if _, pending := s.pendingPriority[key]; !priority && pending {
			continue
		}
		window := s.windowFor(key, sourceIDs[key], now, interval)
		selected := s.takeDiscoveredWork(key, sourceIDs[key], window, items, priority)
		if len(selected) == 0 {
			continue
		}
		result.Started = true
		for _, item := range selected {
			go s.runItem(key, item, window.started, maxRetries, disabled)
		}
	}
	// Final nonempty marked pages can still observe or retain an eligible turn;
	// clear missing turns only after processing their candidates.
	if priority && s.ordinaryPhase {
		s.clearMissingTurns()
	}
}

func (s *SourceScheduler) takeDiscoveredWork(key string, sourceID int64, window *sourceWindow, items []sourceWork, priority bool) []sourceWork {
	available := items
	if priority {
		available = s.priorityTurn(window, items)
	}
	selected := s.takeWork(window, available, s.policy.ForSource(key, sourceID))
	if priority && len(selected) > 0 {
		window.nextSeries = uuid.Nil
	}
	if priority {
		s.retainPendingPriority(key, sourceID, window, items)
	}
	return selected
}

func (s *SourceScheduler) retainPendingPriority(key string, sourceID int64, window *sourceWindow, items []sourceWork) {
	for _, it := range items {
		if !s.inFlight[it.id] && !window.seen[it.id] {
			s.pendingPriority[key] = sourceID
			if window.nextSeries == uuid.Nil && it.seriesID != window.lastSeries {
				window.nextSeries = it.seriesID
				window.turnSeen = true
			}
		}
	}
}

// A single series hint preserves a discovered fair turn across bounded pages;
// it stores no Chapter backlog and all eligibility is re-read on the next pass.
func (s *SourceScheduler) priorityTurn(window *sourceWindow, items []sourceWork) []sourceWork {
	if window.nextSeries == uuid.Nil {
		return items
	}
	var turn []sourceWork
	for _, item := range items {
		if item.seriesID == window.nextSeries && !s.inFlight[item.id] && !window.seen[item.id] {
			window.turnSeen = true
			turn = append(turn, item)
		}
	}
	return turn
}

func (s *SourceScheduler) clearMissingTurns() {
	for _, window := range s.sources {
		if !window.turnSeen {
			window.nextSeries = uuid.Nil
		}
	}
}

// Earlier marked pages may have filled a slot that completed later in the
// sweep. Rescan only when that source can use another slot; an exhausted or
// busy window waits for its ordinary deadline/completion opportunity.
func (s *SourceScheduler) pendingPriorityCanLaunch() bool {
	for key, sourceID := range s.pendingPriority {
		window := s.sources[key]
		if window.remaining > 0 && window.active < s.policy.ForSource(key, sourceID) {
			return true
		}
	}
	return false
}

// Poll collects finished windows and reports deadlines without querying the
// candidate feed. A coordinator can poll active work frequently without loading
// whole series feeds until a discovery opportunity occurs.
func (s *SourceScheduler) Poll(now time.Time, interval time.Duration) (SourceStep, error) {
	var result SourceStep
	if err := s.collect(&result); err != nil {
		s.recoverWindowError()
		return result, err
	}
	if err := s.ctx.Err(); err != nil {
		return result, err
	}
	result.More = s.discovering
	s.markStepStatus(&result, now, interval)
	return result, nil
}

func (s *SourceScheduler) recoverWindowError() {
	s.Close()
	if s.parent.Err() == nil {
		s.ctx, s.cancel = context.WithCancel(s.parent)
	}
}

func (s *SourceScheduler) markStepStatus(result *SourceStep, now time.Time, interval time.Duration) {
	for key, window := range s.sources {
		result.Running = result.Running || window.running
		if interval > 0 && key != unresolvedTargetKey {
			markWindowDeadline(result, window, now, interval)
		}
	}
}

func markWindowDeadline(result *SourceStep, window *sourceWindow, now time.Time, interval time.Duration) {
	next := window.started.Add(interval)
	if !window.running && window.remaining == 0 && next.After(now) && (result.WakeAt.IsZero() || next.Before(result.WakeAt)) {
		result.WakeAt = next
	}
	if result.NextRunAt.IsZero() || next.Before(result.NextRunAt) {
		result.NextRunAt = next
	}
}

func (s *SourceScheduler) discover(now time.Time, maxRetries int, disabled map[int64]bool) (map[string][]sourceWork, map[string]int64, bool, error) {
	var err error
	var selections []chapter.Selection
	if !s.queuedDone {
		selections, err = chapter.QueuedSelectionsInPriority(s.ctx, s.d.client, wantedScanLimit, s.queuedAfter, !s.ordinaryPhase)
	}
	if err != nil {
		return nil, nil, false, err
	}
	more := len(selections) == wantedScanLimit
	if more {
		s.queuedAfter = selections[len(selections)-1].Chapter
	} else {
		s.queuedAfter = nil
		s.queuedDone = true
	}
	var wantedSelections []chapter.Selection
	var upgrades []*ent.Chapter
	for _, selection := range selections {
		if selection.Chapter.State == entchapter.StateUpgradeAvailable {
			upgrades = append(upgrades, selection.Chapter)
		} else {
			wantedSelections = append(wantedSelections, selection)
		}
	}
	groups := s.d.groupBySource(s.ctx, wantedSelections, maxRetries, now, disabled)
	upgradeGroups, err := s.upgradeGroupsFor(upgrades, maxRetries, now, disabled)
	if err != nil {
		return nil, nil, false, err
	}
	work, sourceIDs := mergeSourceWork(selections, groups, upgradeGroups)
	return work, sourceIDs, more, nil
}

func mergeSourceWork(selections []chapter.Selection, groups, upgradeGroups map[string][]resolvedChapter) (map[string][]sourceWork, map[string]int64) {
	work := make(map[string][]sourceWork, len(groups)+len(upgradeGroups))
	sourceIDs := make(map[string]int64, len(work))
	byID := make(map[uuid.UUID]sourceWork, len(selections))
	keys := make(map[uuid.UUID]string, len(selections))
	for key, items := range groups {
		for i := range items {
			it := items[i]
			byID[it.chapterID] = sourceWork{id: it.chapterID, seriesID: it.seriesID, download: &it, priority: it.priority}
			keys[it.chapterID] = key
			if _, ok := sourceIDs[key]; !ok {
				sourceIDs[key] = it.sourceID
			}
		}
	}
	for key, items := range upgradeGroups {
		for i := range items {
			it := items[i]
			byID[it.chapterID] = sourceWork{id: it.chapterID, seriesID: it.seriesID, priority: it.priority}
			keys[it.chapterID] = key
			if _, ok := sourceIDs[key]; !ok {
				sourceIDs[key] = it.sourceID
			}
		}
	}
	for _, selection := range selections {
		item, ok := byID[selection.Chapter.ID]
		if !ok {
			continue
		}
		work[keys[selection.Chapter.ID]] = append(work[keys[selection.Chapter.ID]], item)
	}
	return work, sourceIDs
}

func (s *SourceScheduler) windowFor(key string, sourceID int64, now time.Time, interval time.Duration) *sourceWindow {
	window := s.sources[key]
	renew := window == nil || (!window.running && interval > 0 && !now.Before(window.started.Add(interval)))
	if renew {
		lastSeries := uuid.Nil
		nextSeries := uuid.Nil
		if window != nil {
			lastSeries = window.lastSeries
			nextSeries = window.nextSeries
		}
		window = &sourceWindow{started: now, remaining: BatchPerSource(s.policy.ForSource(key, sourceID)), seen: make(map[uuid.UUID]bool), lastSeries: lastSeries, nextSeries: nextSeries}
		s.sources[key] = window
	}
	if key == unresolvedTargetKey {
		window.remaining = wantedScanLimit
	}
	return window
}

func (s *SourceScheduler) takeWork(window *sourceWindow, items []sourceWork, concurrency int) []sourceWork {
	items = prioritizeSourceWork(items, window.lastSeries)
	slots := min(window.remaining, max(0, concurrency-window.active))
	selected := make([]sourceWork, 0, min(len(items), slots))
	for _, it := range items {
		if len(selected) >= slots {
			break
		}
		if s.inFlight[it.id] || window.seen[it.id] {
			continue
		}
		selected = append(selected, it)
	}
	if len(selected) == 0 {
		return nil
	}
	window.remaining -= len(selected)
	window.active += len(selected)
	window.running = window.active > 0
	window.lastSeries = selected[len(selected)-1].seriesID
	for _, it := range selected {
		s.inFlight[it.id] = true
		window.seen[it.id] = true
	}
	return selected
}

func (s *SourceScheduler) collect(result *SourceStep) error {
	for {
		select {
		case completion := <-s.done:
			s.sources[completion.key].active--
			s.sources[completion.key].running = s.sources[completion.key].active > 0
			for _, id := range completion.ids {
				delete(s.inFlight, id)
			}
			result.Completed = true
			result.Upgraded += completion.upgraded
			if completion.err != nil {
				s.cancel()
				return completion.err
			}
		default:
			return nil
		}
	}
}

func (s *SourceScheduler) runItem(key string, item sourceWork, now time.Time, maxRetries int, disabled map[int64]bool) {
	ctx := withEventSink(s.ctx, s.d.newEventSink())
	var upgraded int
	var err error
	if item.download != nil {
		s.d.downloadResolved(ctx, *item.download, maxRetries, now, s.limiter, s.global)
	} else {
		var completed bool
		completed, err = s.d.upgradeWith(ctx, item.id, s.limiter, disabled, s.global)
		if completed {
			upgraded = 1
		}
	}
	s.d.flushEventSink(ctx, eventSinkFrom(ctx))
	s.done <- sourceCompletion{key: key, ids: []uuid.UUID{item.id}, upgraded: upgraded, err: err}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// prioritizeSourceWork retains series rotation within the fixed priority and
// ordinary partitions. Both downloads and upgrades spend the same allowance.
func prioritizeSourceWork(items []sourceWork, after uuid.UUID) []sourceWork {
	var priority, ordinary []sourceWork
	for _, item := range items {
		if item.priority {
			priority = append(priority, item)
		} else {
			ordinary = append(ordinary, item)
		}
	}
	return append(rotateWorkKind(priority, after), rotateSourceSeries(ordinary, after)...)
}

// Close stops admission and joins every window before the caller closes engine
// hosts or the database. Cancellation errors from owned fetches retain their
// existing state/fallback handling; the coordinator does not rewrite chapters.
func (s *SourceScheduler) Close() {
	s.cancel()
	for len(s.inFlight) > 0 {
		completion := <-s.done
		s.sources[completion.key].active--
		s.sources[completion.key].running = s.sources[completion.key].active > 0
		for _, id := range completion.ids {
			delete(s.inFlight, id)
		}
	}
}

func rotateSourceSeries(items []sourceWork, after uuid.UUID) []sourceWork {
	return rotateWorkKind(items, after)
}

func rotateWorkKind(items []sourceWork, after uuid.UUID) []sourceWork {
	bySeries := make(map[uuid.UUID][]sourceWork)
	var order []uuid.UUID
	for _, it := range items {
		if _, ok := bySeries[it.seriesID]; !ok {
			order = append(order, it.seriesID)
		}
		bySeries[it.seriesID] = append(bySeries[it.seriesID], it)
	}
	start := 0
	for i, id := range order {
		if id == after {
			start = (i + 1) % len(order)
			break
		}
	}
	result := make([]sourceWork, 0, len(items))
	for round := 0; len(result) < len(items); round++ {
		for i := range order {
			queue := bySeries[order[(start+i)%len(order)]]
			if round < len(queue) {
				result = append(result, queue[round])
			}
		}
	}
	return result
}

func (s *SourceScheduler) upgradeGroupsFor(chapters []*ent.Chapter, maxRetries int, now time.Time, disabled map[int64]bool) (map[string][]resolvedChapter, error) {
	candidates, err := chapter.RankedLiveCandidatesForMany(s.ctx, s.d.client, chapters, maxRetries, now, disabled)
	if err != nil {
		return nil, fmt.Errorf("rank upgrade scheduling page: %w", err)
	}
	snapshot := loadBreakerSnapshot(s.ctx, s.d.gate)
	groups := make(map[string][]resolvedChapter)
	for _, ch := range chapters {
		key := unresolvedTargetKey
		var sourceID int64
		if best := bestGatedCandidate(snapshot, candidates[ch.ID], now); best != nil {
			key = canonicalSourceKey(best.SeriesProvider)
			sourceID = linkedSourceID(best.SeriesProvider)
		}
		groups[key] = append(groups[key], resolvedChapter{chapterID: ch.ID, seriesID: ch.SeriesID, sourceID: sourceID, priority: ch.DownloadPriority})
	}
	for key, items := range groups {
		groups[key] = roundRobinBySeries(items)
	}
	return groups, nil
}
