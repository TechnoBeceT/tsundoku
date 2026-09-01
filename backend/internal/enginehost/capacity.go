package enginehost

import (
	"context"
	"fmt"
	"sort"
	"syscall"
	"time"

	"github.com/technobecet/tsundoku/internal/engineroute"
)

const (
	maxKCEFProcessGroups  = 2
	groupExitPollInterval = 5 * time.Millisecond
)

// kcefProcessGroup is one capacity reservation from immediately before process
// start until both the JVM has been reaped and the OS confirms its complete
// process group is absent. A nil proc is the short starting window before Start
// returns; retiring groups remain in the ledger until confirmed absent.
type kcefProcessGroup struct {
	profileKey string
	proc       RunningProcess
	retiring   bool
}

// PrepareProfiles freezes one reconcile pass's KCEF admission set. Existing
// desired instances retain their slots; obsolete KCEF generations are degraded,
// terminated, and reaped before new candidates are considered; remaining
// candidates are admitted in canonical key order. KCEF-off profiles bypass this
// ledger entirely.
func (l *Launcher) PrepareProfiles(ctx context.Context, desired []engineroute.Profile) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}

	ordered := append([]engineroute.Profile(nil), desired...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Key < ordered[j].Key })
	desiredKCEFKeys := make(map[string]bool, len(ordered))
	for _, profile := range ordered {
		if profile.KCEFEnabled {
			desiredKCEFKeys[profile.Key] = true
		}
	}

	if ctx.Err() == nil {
		l.retireObsoleteKCEFLocked(ctx, desiredKCEFKeys)
		l.reapRetiringKCEFLocked(ctx)
	} else {
		l.releaseAbsentKCEFGroupsLocked()
	}

	admitted := make(map[string]bool)
	retainedReady := make(map[string]bool)
	for _, profile := range ordered {
		if !profile.KCEFEnabled {
			continue
		}
		if instance, ok := l.instances[profile.Key]; ok && instance.profile.KCEFEnabled && alive(instance.proc) {
			admitted[profile.Key] = true
			retainedReady[profile.Key] = true
		}
	}

	managedSlots := maxKCEFProcessGroups - l.defaultKCEFReservationLocked()
	for _, profile := range ordered {
		if len(admitted) >= managedSlots {
			break
		}
		if !profile.KCEFEnabled || admitted[profile.Key] {
			continue
		}
		admitted[profile.Key] = true
	}

	// A dead desired generation is replaceable capacity, not a ready-profile
	// retention claim. Retire any such generation that lost canonical admission
	// before a winning replacement can start.
	if ctx.Err() == nil {
		l.retireUnadmittedDesiredKCEFLocked(ctx, ordered, admitted)
	}

	// Failed-start and not-yet-reaped groups have no admitted instance to replace
	// in-place, so each consumes a slot outright. Trim only non-retained candidates
	// from the end, preserving ready profiles and canonical-key priority.
	for {
		admissionLimit := managedSlots - l.kcefGroupsOutsideAdmissionLocked(admitted)
		if admissionLimit < 0 {
			admissionLimit = 0
		}
		if len(admitted) <= admissionLimit {
			break
		}
		dropped := false
		for i := len(ordered) - 1; i >= 0; i-- {
			key := ordered[i].Key
			if !admitted[key] || retainedReady[key] {
				continue
			}
			delete(admitted, key)
			dropped = true
			if ctx.Err() == nil {
				l.retireUnadmittedDesiredKCEFLocked(ctx, ordered, admitted)
			}
			break
		}
		if !dropped {
			break
		}
	}
	l.preparedKCEF = admitted
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
	inside := make(map[*kcefProcessGroup]bool, len(admitted))
	for key := range admitted {
		if instance, ok := l.instances[key]; ok && instance.kcefGroup != nil {
			inside[instance.kcefGroup] = true
		}
	}
	outside := 0
	for group := range l.kcefGroups {
		if !inside[group] {
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
	l.stopInstanceLocked(ctx, instance)
	delete(l.instances, key)
	l.preparedRetiredSources = append(l.preparedRetiredSources, instance.profile.SourceIDs...)
}

func (l *Launcher) reapRetiringKCEFLocked(ctx context.Context) {
	groups := make([]*kcefProcessGroup, 0, len(l.kcefGroups))
	for group := range l.kcefGroups {
		if group.retiring {
			groups = append(groups, group)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].profileKey < groups[j].profileKey })
	for _, group := range groups {
		if ctx.Err() != nil {
			return
		}
		if group.proc != nil && terminateProcessGroup(ctx, group.proc, l.stopGrace) {
			delete(l.kcefGroups, group)
		}
	}
	l.releaseAbsentKCEFGroupsLocked()
}

func (l *Launcher) releaseAbsentKCEFGroupsLocked() {
	for group := range l.kcefGroups {
		if group.proc != nil && processReaped(group.proc) && processGroupAbsent(group.proc) {
			delete(l.kcefGroups, group)
		}
	}
}

func (l *Launcher) reserveKCEFGroupLocked(profileKey string) (*kcefProcessGroup, error) {
	if l.preparedKCEF != nil && !l.preparedKCEF[profileKey] {
		return nil, fmt.Errorf("%w: profile %q was not admitted", ErrKCEFCapacity, profileKey)
	}
	l.releaseAbsentKCEFGroupsLocked()
	if l.defaultKCEFReservationLocked()+len(l.kcefGroups) >= maxKCEFProcessGroups {
		return nil, fmt.Errorf("%w: profile %q", ErrKCEFCapacity, profileKey)
	}
	group := &kcefProcessGroup{profileKey: profileKey}
	l.kcefGroups[group] = struct{}{}
	return group, nil
}

func (l *Launcher) cancelStartingKCEFGroupLocked(group *kcefProcessGroup) {
	if group != nil && group.proc == nil {
		// Start returned an error, so no process group was created. This is the sole
		// reservation release that does not require an ESRCH probe.
		delete(l.kcefGroups, group)
	}
}

func (l *Launcher) stopInstanceLocked(ctx context.Context, instance *managedInstance) bool {
	if instance.kcefGroup != nil {
		instance.kcefGroup.retiring = true
	}
	gone := terminateProcessGroup(ctx, instance.proc, l.stopGrace)
	if gone && instance.kcefGroup != nil {
		delete(l.kcefGroups, instance.kcefGroup)
	}
	return gone
}

func (l *Launcher) stopDetachedInstance(ctx context.Context, instance *managedInstance) {
	if instance.kcefGroup != nil {
		l.mu.Lock()
		instance.kcefGroup.retiring = true
		l.mu.Unlock()
	}
	gone := terminateProcessGroup(ctx, instance.proc, l.stopGrace)
	if gone && instance.kcefGroup != nil {
		l.mu.Lock()
		delete(l.kcefGroups, instance.kcefGroup)
		l.mu.Unlock()
	}
}

func lingeringProcessGroupError(instance *managedInstance) error {
	if instance.kcefGroup != nil {
		return fmt.Errorf("%w: prior process group for profile %q remains", ErrKCEFCapacity, instance.key)
	}
	return fmt.Errorf("enginehost: prior process group for profile %q remains", instance.key)
}

// terminateProcessGroup sends TERM and then KILL to the complete managed group,
// waiting at most one grace interval after each signal. It reports success only
// after the JVM's one Wait has completed and GroupExists confirms ESRCH.
func terminateProcessGroup(ctx context.Context, proc RunningProcess, grace time.Duration) bool {
	if ctx.Err() != nil {
		return forceKillCanceledProcessGroup(proc)
	}
	_ = proc.Signal(syscall.SIGTERM)
	if waitForProcessGroupExit(ctx, proc, grace) {
		return true
	}
	if ctx.Err() != nil {
		return forceKillCanceledProcessGroup(proc)
	}
	_ = proc.Kill()
	return waitForProcessGroupExit(ctx, proc, grace)
}

func forceKillCanceledProcessGroup(proc RunningProcess) bool {
	// Cancellation bounds the caller's wait, not ownership cleanup. Deliver KILL
	// before returning promptly; the one process reaper remains active, and KCEF
	// capacity stays reserved unless absence is already provable.
	_ = proc.Kill()
	return processReaped(proc) && processGroupAbsent(proc)
}

// killProcessGroup is the failed-start path: an instance that never passed its
// readiness contract has no graceful work to drain, so it receives group KILL
// immediately and is still bounded by the same reap/absence wait.
func killProcessGroup(ctx context.Context, proc RunningProcess, grace time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	_ = proc.Kill()
	return waitForProcessGroupExit(ctx, proc, grace)
}

func waitForProcessGroupExit(ctx context.Context, proc RunningProcess, limit time.Duration) bool {
	reaped := processReaped(proc)
	if reaped && processGroupAbsent(proc) {
		return true
	}
	if limit <= 0 {
		return false
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	ticker := time.NewTicker(min(groupExitPollInterval, limit))
	defer ticker.Stop()
	done := proc.Done()
	if reaped {
		// A closed channel is always selectable. Disable this arm after observing
		// the one exact Wait so descendant-only groups poll at the bounded cadence
		// instead of spinning until ESRCH.
		done = nil
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return reaped && processGroupAbsent(proc)
		case <-done:
			reaped = true
			done = nil
			if processGroupAbsent(proc) {
				return true
			}
		case <-ticker.C:
			if reaped && processGroupAbsent(proc) {
				return true
			}
		}
	}
}

func processReaped(proc RunningProcess) bool {
	select {
	case <-proc.Done():
		return true
	default:
		return false
	}
}

func processGroupAbsent(proc RunningProcess) bool {
	exists, err := proc.GroupExists()
	return err == nil && !exists
}
