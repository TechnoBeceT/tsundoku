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

# ── Tunables ─────────────────────────────────────────────────────────────────
# The probe cadence and timeout deliberately MATCH the image's HEALTHCHECK, so the
# watchdog and the container agree on what "down" means rather than disagreeing by
# a few seconds and confusing whoever reads the logs afterwards.
WATCHDOG_HEALTH_URL=${WATCHDOG_HEALTH_URL:-http://127.0.0.1:7777/health}
WATCHDOG_PROBE_INTERVAL=${WATCHDOG_PROBE_INTERVAL:-30}
WATCHDOG_PROBE_TIMEOUT=${WATCHDOG_PROBE_TIMEOUT:-5}
WATCHDOG_FAIL_THRESHOLD=${WATCHDOG_FAIL_THRESHOLD:-3}
WATCHDOG_DUMP_SETTLE=${WATCHDOG_DUMP_SETTLE:-3}
WATCHDOG_BLOCKED_MIN=${WATCHDOG_BLOCKED_MIN:-6}
WATCHDOG_TERM_GRACE=${WATCHDOG_TERM_GRACE:-10}
# One cooldown governs BOTH dumps and kills. Without the dump half, a legitimate
# ~20-minute call would be dumped every 90s — roughly 40 thread dumps during exactly
# the operation the discriminator exists to protect.
WATCHDOG_COOLDOWN=${WATCHDOG_COOLDOWN:-600}
WATCHDOG_LOG_MAX=${WATCHDOG_LOG_MAX:-67108864}

# watchdog_log MESSAGE — one prefix for every line this file emits, so an operator
# can grep the container log for the watchdog's decisions and see nothing else.
watchdog_log() {
    echo "watchdog: $1" >&2
}

# watchdog_trim_log — bound the captured engine output. /tmp is on the container's
# writable layer, not a volume, so unbounded growth is real disk pressure. Returns 0
# unconditionally: this runs inside a `set -e` loop and must never abort it.
watchdog_trim_log() {
    size=$(wc -c < "$WATCHDOG_LOG_FILE" 2>/dev/null || echo 0)
    if [ "$size" -gt "$WATCHDOG_LOG_MAX" ]; then
        : > "$WATCHDOG_LOG_FILE"
        watchdog_log "captured engine output exceeded ${WATCHDOG_LOG_MAX} bytes; truncated"
    fi
    return 0
}

# watchdog_stop_engine PID — end the engine-host so supervise_engine can relaunch it
# cleanly. TERM first, KILL only if it has not exited within the grace period. The
# relaunch path (reaping orphaned Chromium children, clearing the singleton files) is
# already owned by supervise_engine and is deliberately NOT duplicated here.
watchdog_stop_engine() {
    watchdog_log "stopping engine-host pid $1 (TERM, then KILL after ${WATCHDOG_TERM_GRACE}s)"
    kill -TERM "$1" 2>/dev/null || true
    waited=0
    while [ "$waited" -lt "$WATCHDOG_TERM_GRACE" ]; do
        if ! kill -0 "$1" 2>/dev/null; then
            watchdog_log "engine-host pid $1 exited on TERM"
            return 0
        fi
        waited=$((waited + 1))
        sleep 1
    done
    kill -KILL "$1" 2>/dev/null || true
    watchdog_log "engine-host pid $1 did not exit on TERM; sent KILL"
    return 0
}

# supervise_engine_health — probe the engine, and when it stops answering, decide from
# a thread dump whether it is deadlocked or merely saturated.
#
# Intended to be backgrounded by entrypoint.sh AFTER the engine's first successful
# /health, so it can never fire against an engine that is still booting.
#
# Every branch is written as an explicit `if`. Under `set -e`, a trailing
# `[ test ] && command` whose test is FALSE returns non-zero and would kill this loop
# the first time the engine is healthy — the exact failure that would make the
# watchdog silently absent when it is finally needed.
supervise_engine_health() {
    fails=0
    last_dump=0
    last_kill=0

    while true; do
        sleep "$WATCHDOG_PROBE_INTERVAL"
        watchdog_trim_log

        if curl -fsS --max-time "$WATCHDOG_PROBE_TIMEOUT" "$WATCHDOG_HEALTH_URL" >/dev/null 2>&1; then
            fails=0
            continue
        fi

        fails=$((fails + 1))
        if [ "$fails" -lt "$WATCHDOG_FAIL_THRESHOLD" ]; then
            continue
        fi
        # Reset here so the threshold counts consecutive failures since the LAST
        # verdict, not since boot — otherwise every subsequent probe re-triggers.
        fails=0

        now=$(date +%s)
        if [ $((now - last_dump)) -lt "$WATCHDOG_COOLDOWN" ]; then
            continue
        fi

        engine_pid=$(cat "$WATCHDOG_PID_FILE" 2>/dev/null || echo "")
        if [ -z "$engine_pid" ] || ! kill -0 "$engine_pid" 2>/dev/null; then
            # No live engine to dump. It either never started or already died, and
            # supervise_engine owns that case.
            watchdog_log "engine-host pid unknown or not running; leaving recovery to supervise_engine"
            continue
        fi

        # Only lines appended AFTER this point belong to the dump we are about to
        # request. Reading the whole file would match a PREVIOUS wedge's dump and
        # restart a perfectly healthy engine.
        offset=$(wc -c < "$WATCHDOG_LOG_FILE" 2>/dev/null || echo 0)

        watchdog_log "/health silent for ${WATCHDOG_FAIL_THRESHOLD} probes; requesting a thread dump from pid ${engine_pid} (GAP-137)"
        kill -3 "$engine_pid" 2>/dev/null || true
        last_dump=$now
        sleep "$WATCHDOG_DUMP_SETTLE"

        tail -c "+$((offset + 1))" "$WATCHDOG_LOG_FILE" > "$WATCHDOG_DUMP_FILE" 2>/dev/null || true
        blocked=$(wedge_blocked_count "$WATCHDOG_DUMP_FILE")

        if [ "$blocked" -lt "$WATCHDOG_BLOCKED_MIN" ]; then
            watchdog_log "${blocked} RPC pool threads blocked (need ${WATCHDOG_BLOCKED_MIN}); engine is SATURATED, not deadlocked — not restarting"
            continue
        fi

        watchdog_log "WEDGE CONFIRMED: ${blocked} RPC pool threads BLOCKED on $(wedge_held_monitor "$WATCHDOG_DUMP_FILE") (GAP-137)"

        if [ $((now - last_kill)) -lt "$WATCHDOG_COOLDOWN" ]; then
            watchdog_log "last restart was $((now - last_kill))s ago; holding off (thrash guard)"
            continue
        fi

        last_kill=$now
        watchdog_stop_engine "$engine_pid"
    done
}
