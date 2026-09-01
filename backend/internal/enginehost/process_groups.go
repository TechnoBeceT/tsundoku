package enginehost

import (
	"context"
	"fmt"
	"sort"
	"syscall"
	"time"
)

const groupExitPollInterval = 5 * time.Millisecond

// ownedProcessGroup is one lifecycle ownership record from immediately before
// process start until both the JVM has been reaped and the OS identity seam
// confirms its complete original group is absent. A nil proc is the short
// starting window before Start returns; retiring groups remain in the ledger
// until confirmed absent. KCEF-off groups stay owned but do not consume the
// browser capacity limit.
type ownedProcessGroup struct {
	profileKey  string
	proc        RunningProcess
	kcefEnabled bool
	retiring    bool
}

func (l *Launcher) reapRetiringProcessGroupsLocked(ctx context.Context) {
	groups := make([]*ownedProcessGroup, 0, len(l.processGroups))
	for group := range l.processGroups {
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
			delete(l.processGroups, group)
		}
	}
	l.releaseAbsentProcessGroupsLocked()
}

func (l *Launcher) hasRetiringKCEFGroupLocked() bool {
	for group := range l.processGroups {
		if group.kcefEnabled && group.retiring {
			return true
		}
	}
	return false
}

func (l *Launcher) releaseAbsentProcessGroupsLocked() {
	for group := range l.processGroups {
		if group.proc != nil && processReaped(group.proc) && processGroupAbsent(group.proc) {
			delete(l.processGroups, group)
		}
	}
}

func (l *Launcher) reserveProcessGroupLocked(profileKey string, kcefEnabled bool) (*ownedProcessGroup, error) {
	if kcefEnabled && l.preparedKCEF != nil && !l.preparedKCEF[profileKey] {
		return nil, fmt.Errorf("%w: profile %q was not admitted", ErrKCEFCapacity, profileKey)
	}
	l.releaseAbsentProcessGroupsLocked()
	if kcefEnabled && l.defaultKCEFReservationLocked()+l.kcefProcessGroupCountLocked() >= maxKCEFProcessGroups {
		return nil, fmt.Errorf("%w: profile %q", ErrKCEFCapacity, profileKey)
	}
	group := &ownedProcessGroup{profileKey: profileKey, kcefEnabled: kcefEnabled}
	l.processGroups[group] = struct{}{}
	return group, nil
}

func (l *Launcher) kcefProcessGroupCountLocked() int {
	count := 0
	for group := range l.processGroups {
		if group.kcefEnabled {
			count++
		}
	}
	return count
}

func (l *Launcher) cancelStartingProcessGroupLocked(group *ownedProcessGroup) {
	if group != nil && group.proc == nil {
		// Start returned an error, so no process group was created. This is the sole
		// reservation release that needs no OS absence proof.
		delete(l.processGroups, group)
	}
}

func (l *Launcher) stopInstanceLocked(ctx context.Context, instance *managedInstance) bool {
	if instance.processGroup != nil {
		instance.processGroup.retiring = true
	}
	gone := terminateProcessGroup(ctx, instance.proc, l.stopGrace)
	if gone && instance.processGroup != nil {
		delete(l.processGroups, instance.processGroup)
	}
	return gone
}

func (l *Launcher) stopDetachedInstance(ctx context.Context, instance *managedInstance) {
	if instance.processGroup != nil {
		l.mu.Lock()
		instance.processGroup.retiring = true
		l.mu.Unlock()
	}
	gone := terminateProcessGroup(ctx, instance.proc, l.stopGrace)
	if gone && instance.processGroup != nil {
		l.mu.Lock()
		delete(l.processGroups, instance.processGroup)
		l.mu.Unlock()
	}
}

func lingeringProcessGroupError(instance *managedInstance) error {
	if instance.processGroup != nil && instance.processGroup.kcefEnabled {
		return fmt.Errorf("%w: prior process group for profile %q remains", ErrKCEFCapacity, instance.key)
	}
	return fmt.Errorf("enginehost: prior process group for profile %q remains", instance.key)
}

// terminateProcessGroup sends TERM and then KILL to the complete managed group,
// waiting at most one grace interval after each signal. It reports success only
// after the JVM's one Wait has completed and the ownership-aware group probe
// confirms the original group absent.
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
	if reapedProcessGroupAbsent(reaped, proc) {
		return true
	}
	if limit <= 0 {
		return false
	}
	deadline := time.Now().Add(limit)
	done := proc.Done()
	if reaped {
		// A closed channel is always selectable. Disable this arm after observing
		// the one exact Wait so descendant-only groups poll at the bounded cadence
		// instead of spinning until absence.
		done = nil
	}
	for {
		delay := min(groupExitPollInterval, time.Until(deadline))
		if delay <= 0 {
			return reapedProcessGroupAbsent(reaped, proc)
		}
		switch waitForProcessGroupEvent(ctx, done, delay) {
		case processGroupWaitCanceled:
			return false
		case processGroupWaitReaped:
			reaped = true
			done = nil
		}
		if reapedProcessGroupAbsent(reaped, proc) {
			return true
		}
	}
}

func reapedProcessGroupAbsent(reaped bool, proc RunningProcess) bool {
	return reaped && processGroupAbsent(proc)
}

type processGroupWaitEvent uint8

const (
	processGroupWaitPoll processGroupWaitEvent = iota
	processGroupWaitCanceled
	processGroupWaitReaped
)

func waitForProcessGroupEvent(ctx context.Context, done <-chan struct{}, delay time.Duration) processGroupWaitEvent {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return processGroupWaitCanceled
	case <-done:
		return processGroupWaitReaped
	case <-timer.C:
		return processGroupWaitPoll
	}
}

func processReaped(proc RunningProcess) bool {
	if process, ok := proc.(interface{ Reaped() <-chan struct{} }); ok {
		select {
		case <-process.Reaped():
			return true
		default:
			return false
		}
	}
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
