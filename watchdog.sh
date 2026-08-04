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
# thread's state to its stdout, so the dump is both the evidence AND the decision
# input.
#
# ── The rule: EVERY pool thread blocked, never a majority ────────────────────
# The monitor's OWNER is what separates a deadlock from a busy engine, and the owner
# is exactly what a count of waiters cannot see:
#
#   * True wedge — the owner is OUTSIDE the pool. GAP-137's dump has HTTP-Dispatcher
#     holding the extension's monitor, parked in a network wait that never returns.
#     Nothing then occupies a pool slot, so ALL N pool threads can block.
#   * Merely saturated — the owner IS a pool thread, running a legitimate long call.
#     It holds a slot while it runs, so at most N-1 threads can ever be blocked.
#
# A majority threshold cannot tell those apart: the shipped `blocked >= 6 (of 8)`
# matched BOTH, and killed a healthy, progressing engine in testing. The rule is
# therefore `blocked == seen` — every RPC pool thread we can see is blocked — which
# excludes saturation structurally rather than by choosing a luckier number.
#
# It is expressed against the threads actually SEEN, never a literal 8, so resizing
# the pool cannot silently turn it back into a majority rule.
#
# Accepted cost, stated rather than hidden: a wedge whose monitor holder is itself a
# pool thread leaves seen-1 blocked and will NOT fire. The verdict is logged either
# way, so that case is visible in the container log instead of being a mystery.
#
# ── Identifying the RPC pool ─────────────────────────────────────────────────
# Two accepted thread-name shapes, in STRICT precedence order:
#
#   1. "engine-rpc-<n>"        the engine-host's named ThreadFactory. AUTHORITATIVE.
#   2. "pool-<n>-thread-<m>"   the JDK default. FALLBACK ONLY.
#
# The fallback exists because `Executors.newFixedThreadPool(8)` with no factory names
# its threads from a PROCESS-GLOBAL counter in the JDK: any library that creates a
# pool first shifts the RPC pool to pool-2-*, and a predicate hardcoded to pool-1
# then counts zero for a genuine permanent deadlock and never recovers it. That was
# reproduced end to end, so the fallback must not be anchored to pool-1 either.
#
# But the fallback is UNOWNED — it would just as happily count some other library's
# pool. So as soon as ANY engine-rpc-* thread appears in the dump, only those are
# considered and every pool-* thread is ignored. The two sets are never merged.
#
# ── Known limitation (no silent failure) ─────────────────────────────────────
# A JVM started with -Xrs ignores SIGQUIT and prints no dump at all, and the Gradle
# start script honours JAVA_OPTS / TSUNDOKU_ENGINE_HOST_OPTS, so that is reachable
# from configuration. The watchdog then sees no pool threads — and says so, loudly,
# as its own distinct verdict. It never reports "saturated" from zero evidence,
# because a reassuring message is what makes a broken watchdog look healthy.
#
# This file defines functions and defaults ONLY. It starts nothing when sourced, so
# the tests can source it safely.

# Where the engine-host's output is captured. A JVM writes its SIGQUIT dump to its
# own stdout and nowhere else, so without this redirect the dump is unreadable.
WATCHDOG_LOG_FILE=${WATCHDOG_LOG_FILE:-/tmp/engine-host.out}
WATCHDOG_PID_FILE=${WATCHDOG_PID_FILE:-/tmp/engine-host.pid}
WATCHDOG_DUMP_FILE=${WATCHDOG_DUMP_FILE:-/tmp/engine-host.dump}

# wedge_scan FILE
# The whole predicate, in ONE pass. Prints three space-separated fields:
#
#     <seen> <blocked> <monitor>
#
#   seen     RPC pool threads found (see the precedence rule in the header)
#   blocked  how many of those are BLOCKED (on object monitor)
#   monitor  "<address> <class>" of the first monitor an RPC pool thread is waiting
#            on — the single most useful line of evidence in a dump, because it names
#            the extension that wedged — or "unknown"
#
# Three things this function does that are easy to get wrong:
#
#  1. THE MONITOR IS POOL-SCOPED. A dump-wide search returns the FIRST blocked thread
#     in the file, and HotSpot prints JVM-internal threads (Finalizer, Reference
#     Handler) BEFORE application ones. The shipped version therefore named
#     `java.lang.ref.ReferenceQueue$Lock` in the WEDGE CONFIRMED line — sending the
#     next investigator at the JVM's own plumbing instead of the extension.
#
#  2. ONE DUMP AT A TIME. Counts reset on every "Full thread dump" line, and only the
#     LAST COMPLETE dump in the region is reported. Two dumps land in one region
#     routinely: GAP-137's own diagnosis recipe tells an operator to `kill -3`, and
#     that can happen inside the watchdog's own settle window. Summed counts are
#     self-consistent enough to look sane and are simply wrong. A region with no
#     complete dump (nothing terminated by the JVM's "JNI global refs" epilogue)
#     falls back to the partial one, so a JVM whose epilogue we do not recognise
#     degrades to the old behaviour instead of going blind.
#
#  3. A HEADER WITHOUT A STATE LINE COUNTS AS SEEN, NOT AS BLOCKED. The BLOCKED test
#     applies to the line IMMEDIATELY after the thread header, so a dump truncated
#     mid-write raises `seen` without raising `blocked` — and `blocked == seen` then
#     cannot fire. Truncation can only ever make this predicate more conservative.
#
# A missing or unreadable file prints "0 0 unknown".
wedge_scan() {
    if [ ! -r "$1" ]; then
        echo "0 0 unknown"
        return 0
    fi
    # The header match requires the closing quote to be followed by a SPACE, so only
    # a real thread header matches. HotSpot's deadlock epilogue quotes thread names
    # at the start of a line too ("pool-2-thread-1":), and that section is not a
    # thread list. That check is belt-and-braces and no fixture pins it: the epilogue
    # is printed AFTER the "JNI global refs" line that ends a dump, so the section it
    # would corrupt has already been committed. It earns its place only on the
    # fallback path below, where a JVM whose epilogue line we do not recognise leaves
    # the counts uncommitted.
    awk '
        function pick() {
            if (rpc_seen > 0) {
                out_seen = rpc_seen; out_blocked = rpc_blocked; out_monitor = rpc_monitor
            } else {
                out_seen = pool_seen; out_blocked = pool_blocked; out_monitor = pool_monitor
            }
        }
        /^Full thread dump/ {
            started = 1
            rpc_seen = 0; rpc_blocked = 0; rpc_monitor = ""
            pool_seen = 0; pool_blocked = 0; pool_monitor = ""
            kind = ""; pending = 0
            next
        }
        /^JNI global ref/ {
            if (started) {
                pick()
                complete_seen = out_seen
                complete_blocked = out_blocked
                complete_monitor = out_monitor
                have_complete = 1
            }
            next
        }
        /^"/ {
            pending = 0; kind = ""
            if ($0 ~ /^"engine-rpc-[0-9]+" /) { kind = "rpc"; rpc_seen++; pending = 1 }
            else if ($0 ~ /^"pool-[0-9]+-thread-[0-9]+" /) { kind = "pool"; pool_seen++; pending = 1 }
            next
        }
        pending == 1 {
            if ($0 ~ /java\.lang\.Thread\.State: BLOCKED \(on object monitor\)/) {
                if (kind == "rpc") { rpc_blocked++ } else { pool_blocked++ }
            }
            pending = 0
            next
        }
        /^$/ { kind = ""; next }
        kind != "" && /- waiting to lock </ {
            if (match($0, /<0x[0-9a-fA-F]+> \(a [^)]*\)/)) {
                tok = substr($0, RSTART, RLENGTH)
                addr = substr(tok, 2, index(tok, ">") - 2)
                cls = substr(tok, index(tok, "(a ") + 3)
                cls = substr(cls, 1, length(cls) - 1)
                if (kind == "rpc") {
                    if (rpc_monitor == "") { rpc_monitor = addr " " cls }
                } else {
                    if (pool_monitor == "") { pool_monitor = addr " " cls }
                }
            }
            next
        }
        END {
            if (have_complete) {
                out_seen = complete_seen
                out_blocked = complete_blocked
                out_monitor = complete_monitor
            } else if (started) {
                pick()
            } else {
                out_seen = 0; out_blocked = 0; out_monitor = ""
            }
            if (out_monitor == "") { out_monitor = "unknown" }
            print (out_seen + 0) " " (out_blocked + 0) " " out_monitor
        }
    ' "$1"
}

# wedge_held_monitor FILE — just the monitor field of wedge_scan, for reading a saved
# dump by hand and for the tests. The supervision loop does NOT call this: it parses
# all three fields out of its single wedge_scan pass, because a wedged JVM's dump can
# be megabytes and reading it twice buys nothing.
wedge_held_monitor() {
    monitor=$(wedge_scan "$1")
    monitor=${monitor#* }
    monitor=${monitor#* }
    echo "$monitor"
}

# wedge_verdict SEEN BLOCKED — the decision, kept separate from both the parsing and
# the loop so it can be tested directly. Prints exactly one of:
#
#   insufficient  too few RPC pool threads to judge anything (see WATCHDOG_POOL_MIN_SEEN)
#   saturated     some are blocked, some are not — the engine is busy, not deadlocked
#   wedge         every RPC pool thread we can see is blocked — restart it
#
# There is deliberately no "blocked count" knob. A threshold is what conflated a
# wedge with saturation, and a leftover tunable that looks authoritative is worse
# than no tunable at all.
wedge_verdict() {
    v_seen=${1:-0}
    v_blocked=${2:-0}
    if [ "$v_seen" -lt "$WATCHDOG_POOL_MIN_SEEN" ]; then
        echo "insufficient"
    elif [ "$v_blocked" -eq "$v_seen" ]; then
        echo "wedge"
    else
        echo "saturated"
    fi
    return 0
}

# ── Tunables ─────────────────────────────────────────────────────────────────
# The probe cadence and timeout deliberately MATCH the image's HEALTHCHECK, so the
# watchdog and the container agree on what "down" means rather than disagreeing by
# a few seconds and confusing whoever reads the logs afterwards.
#
# The PORT must track the entrypoint's `ENGINE_PORT=${TSUNDOKU_ENGINE_PORT:-7777}`,
# which is why it is derived from the same variable rather than hardcoded. If the two
# ever drift, the watchdog probes a port nothing is listening on, reads a perfectly
# healthy engine as permanently silent, and dumps then KILLS it once every cooldown
# forever. An explicit WATCHDOG_HEALTH_URL still wins, so an operator can point the
# probe somewhere else entirely.
WATCHDOG_HEALTH_URL=${WATCHDOG_HEALTH_URL:-http://127.0.0.1:${TSUNDOKU_ENGINE_PORT:-7777}/health}
WATCHDOG_PROBE_INTERVAL=${WATCHDOG_PROBE_INTERVAL:-30}
WATCHDOG_PROBE_TIMEOUT=${WATCHDOG_PROBE_TIMEOUT:-5}
WATCHDOG_FAIL_THRESHOLD=${WATCHDOG_FAIL_THRESHOLD:-3}
WATCHDOG_DUMP_SETTLE=${WATCHDOG_DUMP_SETTLE:-3}
# The floor under `blocked == seen`. With one visible thread that equality is
# trivially satisfiable, so a dump truncated after a single blocked header would
# restart the engine on the thinnest possible evidence. Two is the smallest number
# that makes the rule say something.
WATCHDOG_POOL_MIN_SEEN=${WATCHDOG_POOL_MIN_SEEN:-2}
WATCHDOG_TERM_GRACE=${WATCHDOG_TERM_GRACE:-10}
# One cooldown governs BOTH dumps and kills. Without the dump half, a legitimate
# ~20-minute call would be dumped every 90s — roughly 40 thread dumps during exactly
# the operation the discriminator exists to protect.
WATCHDOG_COOLDOWN=${WATCHDOG_COOLDOWN:-600}
WATCHDOG_LOG_MAX=${WATCHDOG_LOG_MAX:-67108864}
# How long to wait before re-entering the health loop if it ever exits. Matched to
# the probe interval so a loop stuck in a restart cycle cannot spin.
WATCHDOG_RELOOP_DELAY=${WATCHDOG_RELOOP_DELAY:-30}

# watchdog_log MESSAGE — one prefix for every line this file emits, so an operator
# can grep the container log for the watchdog's decisions and see nothing else.
watchdog_log() {
    echo "watchdog: $1" >&2
}

# watchdog_log_size — byte size of the captured engine output, or 0 if it cannot be
# read. ONE helper, because both callers must use the same redirect order:
#
# `2>/dev/null` comes BEFORE the input redirect deliberately. Redirections are
# applied left to right, so with the input first the SHELL reports the failed open
# ("No such file or directory") on the still-attached stderr before the discard takes
# effect — one spurious line per probe in the only diagnostic channel this container
# has. Do not "tidy" the order back.
watchdog_log_size() {
    wc -c 2>/dev/null < "$WATCHDOG_LOG_FILE" || echo 0
}

# watchdog_trim_log — bound the captured engine output. /tmp is on the container's
# writable layer, not a volume, so unbounded growth is real disk pressure. Returns 0
# unconditionally: this runs inside a `set -e` loop and must never abort it.
watchdog_trim_log() {
    size=$(watchdog_log_size)
    if [ "${size:-0}" -gt "$WATCHDOG_LOG_MAX" ]; then
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

# watchdog_health_loop — probe the engine, and when it stops answering, decide from a
# thread dump whether it is deadlocked or merely saturated.
#
# Call it through supervise_engine_health, never directly: that wrapper is what
# restarts this loop if it ever exits.
#
# EVERY command substitution here is guarded with a fallback. Under the `set -e` this
# inherits from entrypoint.sh, an unguarded `x=$(cmd)` that fails takes the whole
# backgrounded loop down with NO log line — and a transient fork failure is exactly
# what accompanies the memory-pressured, CEF-heavy JVM this watches. That was
# reproduced. For the same reason every branch is an explicit `if`: a trailing
# `[ test ] && command` whose test is false returns non-zero and would kill the loop
# the first time the engine is healthy.
watchdog_health_loop() {
    fails=0
    # Cooldown state lives in globals initialised only when unset, so a loop that
    # dies and is re-entered by supervise_engine_health does not forget when it last
    # dumped or killed — a forgetful restart would walk straight past the thrash
    # guard.
    watchdog_last_dump=${watchdog_last_dump:-0}
    watchdog_last_kill=${watchdog_last_kill:-0}

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

        now=$(date +%s 2>/dev/null || echo 0)
        now=${now:-0}
        if [ $((now - watchdog_last_dump)) -lt "$WATCHDOG_COOLDOWN" ]; then
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
        offset=$(watchdog_log_size)
        offset=${offset:-0}

        watchdog_log "/health silent for ${WATCHDOG_FAIL_THRESHOLD} probes; requesting a thread dump from pid ${engine_pid} (GAP-137)"
        kill -3 "$engine_pid" 2>/dev/null || true
        watchdog_last_dump=$now
        sleep "$WATCHDOG_DUMP_SETTLE"

        tail -c "+$((offset + 1))" "$WATCHDOG_LOG_FILE" > "$WATCHDOG_DUMP_FILE" 2>/dev/null || true
        scan=$(wedge_scan "$WATCHDOG_DUMP_FILE" 2>/dev/null || echo "0 0 unknown")
        scan=${scan:-0 0 unknown}
        seen=${scan%% *}
        scan_rest=${scan#* }
        blocked=${scan_rest%% *}
        monitor=${scan_rest#* }
        seen=${seen:-0}
        blocked=${blocked:-0}

        verdict=$(wedge_verdict "$seen" "$blocked" || echo "insufficient")
        # Three distinct verdicts, never one reassuring line covering all of them.
        # The shipped single message ("engine is SATURATED, not deadlocked") was
        # emitted even when the dump had been parsed into nothing at all, which made
        # a blind watchdog read as a healthy one in the logs.
        if [ "$verdict" = "insufficient" ]; then
            if [ "$seen" -eq 0 ]; then
                watchdog_log "WARNING: no RPC pool threads found in the dump region — the watchdog cannot tell wedged from busy. The thread names may have changed, the dump may not have landed within ${WATCHDOG_DUMP_SETTLE}s, or the JVM may be running with -Xrs (which ignores SIGQUIT). Not restarting (GAP-137)"
            else
                watchdog_log "WARNING: only ${seen} RPC pool thread(s) in the dump region (need ${WATCHDOG_POOL_MIN_SEEN} to judge); the dump looks truncated. Not restarting (GAP-137)"
            fi
            continue
        fi
        if [ "$verdict" = "saturated" ]; then
            watchdog_log "${blocked} of ${seen} RPC pool threads BLOCKED; the rest are running — engine is SATURATED, not deadlocked — not restarting"
            continue
        fi

        watchdog_log "WEDGE CONFIRMED: all ${seen} RPC pool threads BLOCKED on ${monitor} (GAP-137)"

        if [ $((now - watchdog_last_kill)) -lt "$WATCHDOG_COOLDOWN" ]; then
            watchdog_log "last restart was $((now - watchdog_last_kill))s ago; holding off (thrash guard)"
            continue
        fi

        watchdog_last_kill=$now
        watchdog_stop_engine "$engine_pid"
    done
}

# supervise_engine_health — what entrypoint.sh backgrounds. Supervises the health
# loop the same way supervise_engine supervises the engine.
#
# A watchdog that can die silently is worse than none: nothing else in the container
# watches it, so its absence looks exactly like "no wedge has happened yet". The
# guards inside watchdog_health_loop are the primary defence; this is the last one.
#
# Note that calling the loop as `... || true` also suspends `set -e` inside it, which
# is a second reason a stray non-zero status cannot end it. The re-entry is still
# logged, because a loop that keeps restarting is a fault worth seeing.
#
# Intended to be started AFTER the engine's first successful /health, so it can never
# fire against an engine that is still booting.
supervise_engine_health() {
    restarts=0
    while true; do
        if [ "$restarts" -gt 0 ]; then
            watchdog_log "health supervision loop exited unexpectedly (re-entry #${restarts}); resuming in ${WATCHDOG_RELOOP_DELAY}s (GAP-137)"
            sleep "$WATCHDOG_RELOOP_DELAY"
        fi
        restarts=$((restarts + 1))
        watchdog_health_loop || true
    done
}
