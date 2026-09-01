package enginehost

import (
	"context"
	"sort"

	"github.com/technobecet/tsundoku/internal/engineroute"
)

const maxKCEFProcessGroups = 2

type profilePreparation struct {
	launcher   *Launcher
	generation uint64
	protected  map[int64]bool
}

func (p *profilePreparation) CompletePublication() {
	if p == nil || p.launcher == nil {
		return
	}
	l := p.launcher
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.activePreparation != p || l.prepareGeneration != p.generation {
		return
	}
	l.activePreparation = nil
	sourceIDs := make([]int64, 0, len(p.protected))
	for sourceID := range p.protected {
		sourceIDs = append(sourceIDs, sourceID)
	}
	l.restoreEligibleSourcesLocked(sourceIDs)
}

// PrepareProfiles freezes one reconcile pass's KCEF admission set. Existing
// desired instances retain their slots; obsolete KCEF generations are degraded,
// terminated, and reaped before new candidates are considered; remaining
// candidates are admitted in canonical key order. KCEF-off profiles bypass
// browser admission but remain in the lifecycle ledger until teardown is proven.
func (l *Launcher) PrepareProfiles(ctx context.Context, desired []engineroute.Profile) engineroute.ProfilePreparation {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return (*profilePreparation)(nil)
	}
	preparation := l.beginPreparationLocked()
	ordered := canonicalProfiles(desired)
	l.reconcileRetiringGroupsLocked(ctx, desiredKCEFKeys(ordered))
	admitted, retainedReady := l.readyKCEFAdmissionLocked(ordered)
	managedSlots := maxKCEFProcessGroups - l.defaultKCEFReservationLocked()
	l.fillKCEFAdmissionLocked(ordered, admitted, managedSlots)
	l.retireDisplacedKCEFLocked(ctx, ordered, admitted, retainedReady)
	l.trimKCEFAdmissionLocked(ctx, ordered, admitted, retainedReady, managedSlots)
	l.preparedKCEF = admitted
	return preparation
}

func (l *Launcher) beginPreparationLocked() *profilePreparation {
	l.prepareGeneration++
	preparation := &profilePreparation{
		launcher: l, generation: l.prepareGeneration, protected: map[int64]bool{},
	}
	if l.activePreparation != nil {
		for sourceID := range l.activePreparation.protected {
			preparation.protected[sourceID] = true
		}
	}
	l.activePreparation = preparation
	return preparation
}

func canonicalProfiles(desired []engineroute.Profile) []engineroute.Profile {
	ordered := append([]engineroute.Profile(nil), desired...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Key < ordered[j].Key })
	return ordered
}

func desiredKCEFKeys(profiles []engineroute.Profile) map[string]bool {
	desired := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		if profile.KCEFEnabled {
			desired[profile.Key] = true
		}
	}
	return desired
}

func (l *Launcher) reconcileRetiringGroupsLocked(ctx context.Context, desired map[string]bool) {
	if ctx.Err() != nil {
		l.releaseAbsentProcessGroupsLocked()
		return
	}
	l.retireObsoleteKCEFLocked(ctx, desired)
	l.reapRetiringProcessGroupsLocked(ctx)
}

func (l *Launcher) readyKCEFAdmissionLocked(ordered []engineroute.Profile) (map[string]bool, map[string]bool) {
	admitted := make(map[string]bool)
	retained := make(map[string]bool)
	for _, profile := range ordered {
		instance, ok := l.instances[profile.Key]
		if !profile.KCEFEnabled || !ok || !instance.profile.KCEFEnabled || !alive(instance.proc) {
			continue
		}
		admitted[profile.Key] = true
		retained[profile.Key] = true
	}
	return admitted, retained
}

func (l *Launcher) fillKCEFAdmissionLocked(ordered []engineroute.Profile, admitted map[string]bool, managedSlots int) {
	// Any not-yet-reaped obsolete/failed KCEF generation is a replacement
	// barrier, even when the numeric cap has a spare slot.
	if l.hasRetiringKCEFGroupLocked() {
		return
	}
	for _, profile := range ordered {
		if len(admitted) >= managedSlots {
			return
		}
		if profile.KCEFEnabled && !admitted[profile.Key] {
			admitted[profile.Key] = true
		}
	}
}

func (l *Launcher) retireDisplacedKCEFLocked(ctx context.Context, ordered []engineroute.Profile, admitted, retained map[string]bool) {
	if ctx.Err() == nil {
		l.retireUnadmittedDesiredKCEFLocked(ctx, ordered, admitted)
	}
	if !l.hasRetiringKCEFGroupLocked() {
		return
	}
	for key := range admitted {
		if !retained[key] {
			delete(admitted, key)
		}
	}
}

func (l *Launcher) trimKCEFAdmissionLocked(ctx context.Context, ordered []engineroute.Profile, admitted, retained map[string]bool, managedSlots int) {
	for len(admitted) > max(0, managedSlots-l.kcefGroupsOutsideAdmissionLocked(admitted)) {
		key, ok := lastDroppableKCEFKey(ordered, admitted, retained)
		if !ok {
			return
		}
		delete(admitted, key)
		if ctx.Err() == nil {
			l.retireUnadmittedDesiredKCEFLocked(ctx, ordered, admitted)
		}
	}
}

func lastDroppableKCEFKey(ordered []engineroute.Profile, admitted, retained map[string]bool) (string, bool) {
	for i := len(ordered) - 1; i >= 0; i-- {
		key := ordered[i].Key
		if admitted[key] && !retained[key] {
			return key, true
		}
	}
	return "", false
}

func (l *Launcher) retireUnadmittedDesiredKCEFLocked(ctx context.Context, ordered []engineroute.Profile, admitted map[string]bool) {
	for _, profile := range ordered {
		if ctx.Err() != nil {
			return
		}
		instance, ok := l.instances[profile.Key]
		if !profile.KCEFEnabled || admitted[profile.Key] || !ok || !instance.profile.KCEFEnabled || alive(instance.proc) {
			continue
		}
		l.degradeAndRetireInstanceLocked(ctx, profile.Key, instance)
	}
}

func (l *Launcher) kcefGroupsOutsideAdmissionLocked(admitted map[string]bool) int {
	inside := make(map[*ownedProcessGroup]bool, len(admitted))
	for key := range admitted {
		if instance, ok := l.instances[key]; ok && instance.processGroup != nil && instance.processGroup.kcefEnabled {
			inside[instance.processGroup] = true
		}
	}
	outside := 0
	for group := range l.processGroups {
		if group.kcefEnabled && !inside[group] {
			outside++
		}
	}
	return outside
}

func (l *Launcher) defaultKCEFReservationLocked() int {
	if l.cfg.DefaultKCEFEnabled {
		return 1
	}
	return 0
}

func (l *Launcher) retireObsoleteKCEFLocked(ctx context.Context, desired map[string]bool) {
	keys := make([]string, 0, len(l.instances))
	for key, instance := range l.instances {
		if instance.profile.KCEFEnabled && !desired[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}
		instance := l.instances[key]
		l.degradeAndRetireInstanceLocked(ctx, key, instance)
	}
}

func (l *Launcher) degradeAndRetireInstanceLocked(ctx context.Context, key string, instance *managedInstance) {
	// The base route still points at this process until ReconcileNetwork's final
	// publication. The degrade overlay prevents new calls from reaching it while
	// the capacity-critical pre-retirement runs.
	l.degradeLocked(instance)
	if l.activePreparation != nil {
		for _, sourceID := range instance.profile.SourceIDs {
			l.activePreparation.protected[sourceID] = true
		}
	}
	l.stopInstanceLocked(ctx, instance)
	delete(l.instances, key)
}
