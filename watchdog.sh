#!/bin/sh
# watchdog.sh — detect and recover the engine-host request-pool deadlock (GAP-137).
#
# The engine-host serves /health from the SAME fixed 8-thread pool as every source
# call. A source extension that synchronises on itself can park one thread in a
# network wait forever; every other request for that source then piles up behind the
# monitor, the pool drains, and the engine answers NOTHING — every source dies, not
# just the one that wedged. Only a restart clears it.
#
# So "/health is silent" means "the engine cannot serve requests". It does NOT mean
# "the engine is deadlocked": a single legitimate long call (a large series' chapter
# walk runs ~20 minutes) starves the pool identically. Killing on a timeout alone
# would destroy exactly the expensive work this system tries hardest not to repeat.
#
# The discriminator is the thread dump itself. SIGQUIT makes the JVM print every
# thread's state to its stdout; a deadlocked pool shows most of its threads BLOCKED
# on an object monitor, a merely-busy one does not. The dump is therefore both the
# evidence AND the decision input.
#
# This file defines functions and defaults ONLY. It starts nothing when sourced, so
# the tests can source it safely.

# Where the engine-host's output is captured. A JVM writes its SIGQUIT dump to its
# own stdout and nowhere else, so without this redirect the dump is unreadable.
WATCHDOG_LOG_FILE=${WATCHDOG_LOG_FILE:-/tmp/engine-host.out}
WATCHDOG_PID_FILE=${WATCHDOG_PID_FILE:-/tmp/engine-host.pid}
WATCHDOG_DUMP_FILE=${WATCHDOG_DUMP_FILE:-/tmp/engine-host.dump}

# wedge_blocked_count FILE
# Counts RPC pool threads parked on an object monitor in a thread dump.
#
# Strictness is the point. A bare search for "waiting to lock" matches any transient
# contention; this requires BOTH the pool-thread header AND the BLOCKED state line
# that immediately follows it. A dump truncated mid-write leaves a header with no
# state line and correctly counts zero, so a partial dump can never trigger a kill.
#
# Prints an integer. A missing or unreadable file prints 0.
wedge_blocked_count() {
    if [ ! -r "$1" ]; then
        echo 0
        return 0
    fi
    awk '
        /^"pool-1-thread-/ { pending = 1; next }
        pending == 1 {
            if ($0 ~ /java\.lang\.Thread\.State: BLOCKED \(on object monitor\)/) { n++ }
            pending = 0
        }
        END { print n + 0 }
    ' "$1"
}

# wedge_held_monitor FILE
# Extracts the address and class of the first monitor a pool thread is waiting on —
# the single most useful line of evidence in the dump, because it names which source
# extension wedged. Prints "unknown" when no thread is waiting on a monitor.
wedge_held_monitor() {
    monitor=""
    if [ -r "$1" ]; then
        monitor=$(sed -n 's/.*- waiting to lock <\(0x[0-9a-f]*\)> (a \([^)]*\)).*/\1 \2/p' "$1" | head -n 1)
    fi
    if [ -z "$monitor" ]; then
        echo "unknown"
    else
        echo "$monitor"
    fi
}
