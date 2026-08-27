#!/bin/sh
# watchdog_test.sh — fixture tests for the wedge predicate (GAP-137).
#
# Sourcing watchdog.sh must be side-effect free: it defines functions and sets
# defaults, and starts nothing. If this script ever hangs, that contract broke.
#
# EVERY fixture in testdata/watchdog kills at least one mutation of the predicate,
# and each one names the mutation it kills in its own header comment. That rule is
# not ceremony: the first version of this suite had four fixtures of the same shape
# and passed against a bare `grep -c "waiting to lock"`, which is precisely the
# predicate it was written to rule out. A fixture that no mutation can fail is a
# fixture that tests nothing.
#
# Two layers are asserted, because a wedge is only detected if both are right:
#   wedge_scan    — what the dump SAYS (seen / blocked / held monitor)
#   wedge_verdict — what we DO about it (insufficient / saturated / wedge)
set -eu

. "$(dirname "$0")/watchdog.sh"

FIXTURES="$(dirname "$0")/testdata/watchdog"
failures=0

# expect_eq LABEL EXPECTED ACTUAL — report and count a mismatch without aborting,
# so one run reports every failure rather than only the first.
expect_eq() {
    if [ "$2" = "$3" ]; then
        echo "ok   - $1"
    else
        echo "FAIL - $1: expected '$2', got '$3'"
        failures=$((failures + 1))
    fi
}

expect_contains() {
    case "$2" in
        *"$1"*) echo "ok   - $3" ;;
        *)
            echo "FAIL - $3: expected output to contain '$1', got '$2'"
            failures=$((failures + 1))
            ;;
    esac
}

expect_not_contains() {
    case "$2" in
        *"$1"*)
            echo "FAIL - $3: expected output not to contain '$1', got '$2'"
            failures=$((failures + 1))
            ;;
        *) echo "ok   - $3" ;;
    esac
}

# verdict_of FILE — scan a dump and reach a verdict, parsing wedge_scan's output
# exactly the way the supervision loop does. Testing through this shape means the
# parsing is covered too, not just the two functions on either side of it.
verdict_of() {
    scan=$(wedge_scan "$1")
    rest=${scan#* }
    v_s=${rest%% *}
    rest=${rest#* }
    v_b=${rest%% *}
    wedge_verdict "$v_s" "$v_b"
}

MONITOR="0x0000000084271758 eu.kanade.tachiyomi.extension.en.comix.ExtensionGenerated"

# ── The wedge: every RPC pool thread blocked, holder outside the pool ────────
expect_eq "GAP-137 wedge: 8 of 8 pool threads blocked" \
    "jdk 8 8 $MONITOR" "$(wedge_scan "$FIXTURES/dump-wedged.txt")"
expect_eq "GAP-137 wedge is a wedge" \
    "wedge" "$(verdict_of "$FIXTURES/dump-wedged.txt")"

expect_eq "named engine-rpc threads: 8 of 8 blocked" \
    "legacy 8 8 $MONITOR" "$(wedge_scan "$FIXTURES/dump-rpc-wedged.txt")"
expect_eq "named engine-rpc wedge is a wedge" \
    "wedge" "$(verdict_of "$FIXTURES/dump-rpc-wedged.txt")"

# The JDK numbers pools from a process-global counter, so the RPC pool is only
# pool-1 by coincidence. A predicate anchored to pool-1 counts 0 here.
expect_eq "pool-2 fallback: 8 of 8 blocked" \
    "jdk 8 8 $MONITOR" "$(wedge_scan "$FIXTURES/dump-pool2-wedged.txt")"
expect_eq "pool-2 wedge is a wedge" \
    "wedge" "$(verdict_of "$FIXTURES/dump-pool2-wedged.txt")"

# ── Saturation: at least one pool thread still running ──────────────────────
# The HIGH-1 regression. The monitor's holder IS a pool thread, so exactly 7 of 8
# block and the engine is making progress. The shipped `blocked >= 6` rule killed
# this engine.
expect_eq "saturated (holder in pool): 7 of 8 blocked" \
    "jdk 8 7 $MONITOR" "$(wedge_scan "$FIXTURES/dump-saturated-holder-in-pool.txt")"
expect_eq "saturated (holder in pool) is NOT a wedge" \
    "saturated" "$(verdict_of "$FIXTURES/dump-saturated-holder-in-pool.txt")"

expect_eq "busy dump: 8 seen, 0 blocked" \
    "jdk 8 0 unknown" "$(wedge_scan "$FIXTURES/dump-busy.txt")"
expect_eq "busy dump is NOT a wedge" \
    "saturated" "$(verdict_of "$FIXTURES/dump-busy.txt")"

expect_eq "partial pile-up: 5 of 8 blocked" \
    "jdk 8 5 $MONITOR" "$(wedge_scan "$FIXTURES/dump-5of8.txt")"
expect_eq "partial pile-up is NOT a wedge" \
    "saturated" "$(verdict_of "$FIXTURES/dump-5of8.txt")"

# ── Precedence: engine-rpc-* wins outright, pool-* is ignored ───────────────
expect_eq "engine-rpc threads shadow unrelated pool-* threads" \
    "legacy 4 0 unknown" "$(wedge_scan "$FIXTURES/dump-rpc-precedence.txt")"
expect_eq "unrelated blocked pools do not make a wedge" \
    "saturated" "$(verdict_of "$FIXTURES/dump-rpc-precedence.txt")"

# ── One dump at a time ─────────────────────────────────────────────────────
expect_eq "two dumps: only the last is counted" \
    "jdk 8 8 $MONITOR" "$(wedge_scan "$FIXTURES/dump-two-dumps.txt")"
expect_eq "two dumps: the last one's verdict wins" \
    "wedge" "$(verdict_of "$FIXTURES/dump-two-dumps.txt")"

expect_eq "second dump truncated: the last COMPLETE dump is counted" \
    "jdk 8 8 $MONITOR" "$(wedge_scan "$FIXTURES/dump-two-dumps-second-partial.txt")"
expect_eq "second dump truncated: still a wedge" \
    "wedge" "$(verdict_of "$FIXTURES/dump-two-dumps-second-partial.txt")"

# ── Not enough evidence to judge ───────────────────────────────────────────
expect_eq "truncated dump: 1 seen, 0 blocked" \
    "jdk 1 0 unknown" "$(wedge_scan "$FIXTURES/dump-truncated.txt")"
expect_eq "truncated dump cannot be judged" \
    "insufficient" "$(verdict_of "$FIXTURES/dump-truncated.txt")"

expect_eq "single blocked thread: 1 seen, 1 blocked" \
    "jdk 1 1 $MONITOR" "$(wedge_scan "$FIXTURES/dump-single-blocked.txt")"
expect_eq "single blocked thread is NOT enough to restart on" \
    "insufficient" "$(verdict_of "$FIXTURES/dump-single-blocked.txt")"

expect_eq "missing file yields no evidence" \
    "none 0 0 unknown" "$(wedge_scan "$FIXTURES/does-not-exist.txt")"
expect_eq "missing file cannot be judged" \
    "insufficient" "$(verdict_of "$FIXTURES/does-not-exist.txt")"

# ── Lock contention outside the RPC pool ───────────────────────────────────
expect_eq "contended non-pool threads are not counted" \
    "jdk 8 0 unknown" "$(wedge_scan "$FIXTURES/dump-contended.txt")"
expect_eq "contended non-pool threads are not a wedge" \
    "saturated" "$(verdict_of "$FIXTURES/dump-contended.txt")"

# ── The held monitor: the evidence line, pool-scoped ───────────────────────
expect_eq "held monitor is extracted from the wedged dump" \
    "$MONITOR" "$(wedge_held_monitor "$FIXTURES/dump-wedged.txt")"
expect_eq "held monitor is 'unknown' when nothing in the pool is blocked" \
    "unknown" "$(wedge_held_monitor "$FIXTURES/dump-busy.txt")"
# The Finalizer and Cache-Writer in this fixture ARE blocked on monitors, and HotSpot
# prints the Finalizer first. Anything but "unknown" here means the evidence line in
# the WEDGE CONFIRMED log would name the wrong lock.
expect_eq "held monitor ignores blocked threads outside the RPC pool" \
    "unknown" "$(wedge_held_monitor "$FIXTURES/dump-contended.txt")"
expect_eq "held monitor obeys the engine-rpc precedence" \
    "unknown" "$(wedge_held_monitor "$FIXTURES/dump-rpc-precedence.txt")"

# ── Domain-specific named pools ────────────────────────────────────────────
NAMED_POOLS="http 4 4 $MONITOR
source 8 7 $MONITOR
extension 1 1 $MONITOR
deadline 1 0 unknown
network 2 0 unknown"
expect_eq "named pools are reported independently" \
    "$NAMED_POOLS" "$(wedge_scan "$FIXTURES/dump-domain-pools-mixed.txt")"
expect_not_contains "legacy" "$(wedge_scan "$FIXTURES/dump-domain-pools-mixed.txt")" \
    "legacy engine-rpc threads are fallback-only when named domains exist"
expect_not_contains "jdk" "$(wedge_scan "$FIXTURES/dump-domain-pools-mixed.txt")" \
    "unrelated JDK pools are fallback-only when named domains exist"

# ── The decision, directly ─────────────────────────────────────────────────
# `blocked == seen` is the whole rule. These pin it against the two mistakes that
# have already been made: a majority threshold, and firing on a single thread.
expect_eq "verdict: all blocked is a wedge" "wedge" "$(wedge_verdict 8 8)"
expect_eq "verdict: two of two is a wedge" "wedge" "$(wedge_verdict 2 2)"
expect_eq "verdict: seven of eight is saturation" "saturated" "$(wedge_verdict 8 7)"
expect_eq "verdict: six of eight is saturation" "saturated" "$(wedge_verdict 8 6)"
expect_eq "verdict: none blocked is saturation" "saturated" "$(wedge_verdict 8 0)"
expect_eq "verdict: one of one is not enough evidence" "insufficient" "$(wedge_verdict 1 1)"
expect_eq "verdict: nothing seen is not enough evidence" "insufficient" "$(wedge_verdict 0 0)"

# ── Sustained source-pool exhaustion evidence ───────────────────────────────
# The fingerprint deliberately excludes age, counters, and queue depth. It describes
# only physical running work, so an aging-but-identical population stays stable while
# any source-worker replacement resets the episode.
EXPECTED_STATUS_FINGERPRINT="8|8|11:2,22:2,33:2,44:2"
expect_eq "status fingerprint contains approved physical-work fields only" \
    "$EXPECTED_STATUS_FINGERPRINT" "$(status_fingerprint "$FIXTURES/status-exhausted.json")"
expect_eq "completion progress does not alter the running-work fingerprint" \
    "$EXPECTED_STATUS_FINGERPRINT" "$(status_fingerprint "$FIXTURES/status-progressed.json")"
expect_eq "queue-driven order and queue-only sources do not alter the running-work fingerprint" \
    "$EXPECTED_STATUS_FINGERPRINT" "$(status_fingerprint "$FIXTURES/status-queue-reordered.json")"
expect_eq "a changed running source population changes the fingerprint" \
    "8|8|11:2,22:2,33:2,55:2" "$(status_fingerprint "$FIXTURES/status-fingerprint-changed.json")"

sample=$(exhaustion_sample "$FIXTURES/status-exhausted.json" "$FIXTURES/dump-source-first.txt")
expect_eq "an exhaustion sample carries sequence, age, occupancy, fingerprint, and dump population" \
    "41 181001 8 8 $EXPECTED_STATUS_FINGERPRINT engine-source-1,engine-source-2,engine-source-3,engine-source-4,engine-source-5,engine-source-6,engine-source-7,engine-source-8" \
    "$sample"
first_dump=${sample##* }
sixth_sample=$(exhaustion_sample "$FIXTURES/status-exhausted.json" "$FIXTURES/dump-source-sixth.txt")
sixth_dump=${sixth_sample##* }
expect_eq "first and sixth dumps match by source-worker population" "$first_dump" "$sixth_dump"
expect_eq "unknown source-worker state declines evidence" \
    "unknown" "$(exhaustion_sample "$FIXTURES/status-exhausted.json" "$FIXTURES/dump-source-unknown.txt" 2>/dev/null || echo unknown)"
expect_eq "truncated source-worker dump declines evidence" \
    "unknown" "$(exhaustion_sample "$FIXTURES/status-exhausted.json" "$FIXTURES/dump-source-truncated.txt" 2>/dev/null || echo unknown)"
trailing_partial_dump=$(mktemp)
{
    cat "$FIXTURES/dump-source-first.txt"
    cat "$FIXTURES/dump-source-truncated.txt"
} > "$trailing_partial_dump"
expect_eq "a partial latest dump cannot borrow an earlier complete dump" \
    "unknown" "$(exhaustion_sample "$FIXTURES/status-exhausted.json" "$trailing_partial_dump" 2>/dev/null || echo unknown)"
rm -f "$trailing_partial_dump"
expect_eq "malformed status declines evidence" \
    "unknown" "$(exhaustion_sample "$FIXTURES/status-malformed.json" "$FIXTURES/dump-source-first.txt" 2>/dev/null || echo unknown)"
expect_eq "structurally malformed status with every field still declines evidence" \
    "unknown" "$(exhaustion_sample "$FIXTURES/status-malformed-garbage.json" "$FIXTURES/dump-source-first.txt" 2>/dev/null || echo unknown)"
expect_eq "unreadable status declines evidence" \
    "unknown" "$(exhaustion_sample "$FIXTURES/does-not-exist.json" "$FIXTURES/dump-source-first.txt" 2>/dev/null || echo unknown)"
expect_eq "status carrying an unapproved field declines evidence" \
    "unknown" "$(exhaustion_sample "$FIXTURES/status-unapproved.json" "$FIXTURES/dump-source-first.txt" 2>/dev/null || echo unknown)"
oversized_status=$(mktemp)
cp "$FIXTURES/status-exhausted.json" "$oversized_status"
dd if=/dev/zero bs=1024 count=33 2>/dev/null | tr '\000' ' ' >> "$oversized_status"
expect_eq "status input above 32768 bytes declines evidence" \
    "unknown" "$(exhaustion_sample "$oversized_status" "$FIXTURES/dump-source-first.txt" 2>/dev/null || echo unknown)"
rm -f "$oversized_status"

expect_eq "exhaustion verdict accepts the exact six-sample proof" \
    "restart" "$(exhaustion_verdict 6 180001 8 8 1 1)"
expect_eq "exhaustion verdict requires six consecutive samples" \
    "decline" "$(exhaustion_verdict 5 180001 8 8 1 1)"
expect_eq "exhaustion verdict requires oldest strictly above 180000ms" \
    "decline" "$(exhaustion_verdict 6 180000 8 8 1 1)"
expect_eq "exhaustion verdict requires all eight workers running" \
    "decline" "$(exhaustion_verdict 6 180001 7 8 1 1)"
expect_eq "exhaustion verdict rejects a non-eight worker configuration" \
    "decline" "$(exhaustion_verdict 6 180001 7 7 1 1)"
expect_eq "exhaustion verdict requires unchanged progress" \
    "decline" "$(exhaustion_verdict 6 180001 8 8 0 1)"
expect_eq "exhaustion verdict requires matching dump evidence" \
    "decline" "$(exhaustion_verdict 6 180001 8 8 1 0)"
expect_eq "exhaustion verdict fails closed on malformed evidence" \
    "decline" "$(exhaustion_verdict six 180001 8 8 1 1)"

# Diagnostics are reconstructed from approved status fields and redacted thread
# classifications. The raw fixtures contain neither payloads nor local values, but the
# unapproved status fixture proves an injected secret is rejected rather than copied.
diagnostic_file=$(mktemp)
if watchdog_write_exhaustion_diagnostics \
    "$FIXTURES/status-exhausted.json" "$FIXTURES/dump-source-first.txt" \
    "$FIXTURES/dump-source-sixth.txt" 12345 "$diagnostic_file"; then
    diagnostic=$(cat "$diagnostic_file")
    expect_contains "decision=sustained-source-exhaustion" "$diagnostic" \
        "diagnostics record the conservative decision"
    expect_contains "status={\"ready\":true" "$diagnostic" \
        "diagnostics contain a sanitized status snapshot"
    expect_contains "first_dump=engine-source-1 state=" "$diagnostic" \
        "diagnostics contain a redacted first-dump excerpt"
    expect_contains "sixth_dump=engine-source-1 state=" "$diagnostic" \
        "diagnostics contain a redacted sixth-dump excerpt"
    expect_not_contains "must-not-escape" "$diagnostic" \
        "diagnostics never contain unapproved status values"
    diagnostic_status=$(sed -n 's/^status=//p' "$diagnostic_file")
    diagnostic_threads=$(sed -n '/^first_dump=/p;/^sixth_dump=/p' "$diagnostic_file")
    expect_eq "diagnostic status is capped at 32768 bytes" "yes" \
        "$([ "$(printf '%s' "$diagnostic_status" | wc -c)" -le 32768 ] && echo yes || echo no)"
    expect_eq "diagnostic thread excerpts are capped at 262144 bytes" "yes" \
        "$([ "$(printf '%s' "$diagnostic_threads" | wc -c)" -le 262144 ] && echo yes || echo no)"
else
    expect_eq "diagnostics writer accepts complete approved evidence" "success" "failure"
fi
oversized_dump=$(mktemp)
sed '$d' "$FIXTURES/dump-source-first.txt" > "$oversized_dump"
dd if=/dev/zero bs=1024 count=300 2>/dev/null | tr '\000' 'X' >> "$oversized_dump"
printf '\nJNI global refs: 41, weak refs: 0\n' >> "$oversized_dump"
if watchdog_write_exhaustion_diagnostics \
    "$FIXTURES/status-exhausted.json" "$oversized_dump" \
    "$FIXTURES/dump-source-sixth.txt" 12345 "$diagnostic_file"; then
    oversized_excerpt=$(sed -n '/^first_dump=/p;/^sixth_dump=/p' "$diagnostic_file" | wc -c)
    expect_eq "oversized raw dumps still produce at most 262144 diagnostic bytes" "yes" \
        "$([ "$oversized_excerpt" -le 262144 ] && echo yes || echo no)"
else
    expect_eq "oversized raw dumps are reduced to approved evidence" "success" "failure"
fi
rm -f "$oversized_dump"
if watchdog_write_exhaustion_diagnostics \
    "$FIXTURES/status-unapproved.json" "$FIXTURES/dump-source-first.txt" \
    "$FIXTURES/dump-source-sixth.txt" 12345 "$diagnostic_file"; then
    expect_eq "diagnostics reject unapproved status fields" "failure" "success"
else
    echo "ok   - diagnostics reject unapproved status fields"
fi
rm -f "$diagnostic_file"

# ── Deterministic exhaustion loop ───────────────────────────────────────────
stable_loop_result=$(
    (
        WATCHDOG_PROBE_INTERVAL=0
        WATCHDOG_COOLDOWN=600
        tick=0
        sample_count=0
        status_count=0
        dump_count=0
        curl() { return 0; }
        sleep() { tick=$((tick + 1)); [ "$tick" -lt 10 ] || exit 0; }
        watchdog_trim_log() { :; }
        watchdog_probe_health() { sample_count=$((sample_count + 1)); return 0; }
        watchdog_fetch_status() { status_count=$((status_count + 1)); cp "$FIXTURES/status-exhausted.json" "$1"; }
        watchdog_capture_dump() { dump_count=$((dump_count + 1)); cp "$FIXTURES/dump-source-first.txt" "$2"; }
        watchdog_engine_pid() { echo 12345; }
        date() { echo 1000; }
        watchdog_write_exhaustion_diagnostics() { echo "diagnostic-at=$sample_count"; }
        watchdog_stop_engine() { echo "stopped-at-sample=$sample_count statuses=$status_count dumps=$dump_count"; exit 0; }
        watchdog_health_loop
    ) 2>&1
)
expect_contains "stopped-at-sample=6 statuses=7 dumps=2" "$stable_loop_result" \
    "stable exhaustion stops at sample six after final status confirmation"

progressing_loop_result=$(
    (
        WATCHDOG_PROBE_INTERVAL=0
        WATCHDOG_COOLDOWN=600
        tick=0
        sample_count=0
        curl() { return 0; }
        sleep() { tick=$((tick + 1)); [ "$tick" -lt 10 ] || { echo "progressing-ended samples=$sample_count"; exit 0; }; }
        watchdog_trim_log() { :; }
        watchdog_probe_health() { return 0; }
        watchdog_fetch_status() {
            sample_count=$((sample_count + 1))
            if [ $((sample_count % 2)) -eq 0 ]; then
                cp "$FIXTURES/status-progressed.json" "$1"
            else
                cp "$FIXTURES/status-exhausted.json" "$1"
            fi
        }
        watchdog_capture_dump() { cp "$FIXTURES/dump-source-first.txt" "$2"; }
        watchdog_engine_pid() { echo 12345; }
        date() { echo 1000; }
        watchdog_stop_engine() { echo "unexpected-progress-stop"; exit 1; }
        watchdog_health_loop
    ) 2>&1
)
expect_contains "progressing-ended" "$progressing_loop_result" \
    "long progressing work leaves the watchdog loop alive"
expect_not_contains "unexpected-progress-stop" "$progressing_loop_result" \
    "long progressing work never stops the engine"

cooldown_loop_result=$(
    (
        WATCHDOG_PROBE_INTERVAL=0
        WATCHDOG_COOLDOWN=600
        tick=0
        sample_count=0
        stop_count=0
        curl() { return 0; }
        sleep() { tick=$((tick + 1)); [ "$tick" -lt 15 ] || { echo "cooldown-ended stops=$stop_count"; exit 0; }; }
        watchdog_trim_log() { :; }
        watchdog_probe_health() { return 0; }
        watchdog_fetch_status() { sample_count=$((sample_count + 1)); cp "$FIXTURES/status-exhausted.json" "$1"; }
        watchdog_capture_dump() { cp "$FIXTURES/dump-source-first.txt" "$2"; }
        watchdog_engine_pid() { echo 12345; }
        date() { echo 1000; }
        watchdog_write_exhaustion_diagnostics() { :; }
        watchdog_stop_engine() { stop_count=$((stop_count + 1)); }
        watchdog_health_loop
    ) 2>&1
)
expect_contains "cooldown-ended stops=1" "$cooldown_loop_result" \
    "restart cooldown suppresses a second stable-exhaustion stop"

post_dump_progress_result=$(
    (
        WATCHDOG_PROBE_INTERVAL=0
        WATCHDOG_COOLDOWN=600
        tick=0
        status_count=0
        dump_count=0
        curl() { return 0; }
        sleep() {
            if [ "$status_count" -ge 7 ]; then
                echo "post-dump-progress-ended statuses=$status_count dumps=$dump_count"
                exit 0
            fi
            tick=$((tick + 1))
        }
        watchdog_trim_log() { :; }
        watchdog_probe_health() { return 0; }
        watchdog_fetch_status() {
            status_count=$((status_count + 1))
            if [ "$status_count" -eq 7 ]; then
                cp "$FIXTURES/status-progressed.json" "$1"
            else
                cp "$FIXTURES/status-exhausted.json" "$1"
            fi
        }
        watchdog_capture_dump() { dump_count=$((dump_count + 1)); cp "$FIXTURES/dump-source-first.txt" "$2"; }
        watchdog_engine_pid() { echo 12345; }
        date() { echo 1000; }
        watchdog_write_exhaustion_diagnostics() { echo "unexpected-progress-diagnostic"; }
        watchdog_stop_engine() { echo "unexpected-post-dump-progress-stop"; exit 0; }
        watchdog_health_loop
    ) 2>&1
)
expect_contains "post-dump-progress-ended statuses=7 dumps=2" "$post_dump_progress_result" \
    "sixth-dump recovery re-fetches status and observes final progress"
expect_not_contains "unexpected-post-dump-progress-stop" "$post_dump_progress_result" \
    "progress during the sixth dump window resets evidence and declines restart"
expect_not_contains "unexpected-progress-diagnostic" "$post_dump_progress_result" \
    "progress during the sixth dump window cannot produce restart diagnostics"

reset_loop_result=$(
    (
        WATCHDOG_PROBE_INTERVAL=0
        WATCHDOG_COOLDOWN=600
        tick=0
        sample_count=0
        curl() { return 0; }
        sleep() { tick=$((tick + 1)); [ "$tick" -lt 12 ] || { echo "reset-ended samples=$sample_count"; exit 0; }; }
        watchdog_trim_log() { :; }
        watchdog_probe_health() { return 0; }
        watchdog_fetch_status() {
            sample_count=$((sample_count + 1))
            case "$sample_count" in
                1|3|5|7|9) cp "$FIXTURES/status-exhausted.json" "$1" ;;
                2) cp "$FIXTURES/status-progressed.json" "$1" ;;
                4) cp "$FIXTURES/status-fingerprint-changed.json" "$1" ;;
                6) cp "$FIXTURES/status-not-full.json" "$1" ;;
                8) cp "$FIXTURES/status-malformed.json" "$1" ;;
                10) return 1 ;;
                *) cp "$FIXTURES/status-exhausted.json" "$1" ;;
            esac
        }
        watchdog_capture_dump() { cp "$FIXTURES/dump-source-first.txt" "$2"; }
        watchdog_engine_pid() { echo 12345; }
        date() { echo 1000; }
        watchdog_stop_engine() { echo "unexpected-reset-stop"; exit 1; }
        watchdog_health_loop
    ) 2>&1
)
expect_contains "reset-ended" "$reset_loop_result" \
    "progress, fingerprint, non-full, malformed, and unreadable samples reset evidence"
expect_not_contains "unexpected-reset-stop" "$reset_loop_result" \
    "resetting evidence cannot stop the engine"

# ── Continuous late-readiness arming ───────────────────────────────────────
# Run the real loop with only its external effects replaced. The stop marker carries
# the probe number, so firing on failures 1-2 instead of failures 4-5 cannot pass.
late_readiness_result=$(
    (
        WATCHDOG_PROBE_INTERVAL=0
        WATCHDOG_FAIL_THRESHOLD=2
        WATCHDOG_COOLDOWN=0
        WATCHDOG_DUMP_SETTLE=0
        probe_count=0
        curl() {
            probe_count=$((probe_count + 1))
            if [ "$probe_count" -ge 6 ]; then
                echo "late-readiness-loop-ended=$probe_count"
                exit 0
            fi
            [ "$probe_count" -eq 3 ]
        }
        sleep() { :; }
        watchdog_trim_log() { :; }
        watchdog_fetch_status() { return 1; }
        watchdog_log_size() { echo 0; }
        date() { echo 1000; }
        cat() { echo 12345; }
        kill() { return 0; }
        tail() { :; }
        wedge_scan() { echo "http 4 4 $MONITOR"; }
        watchdog_stop_engine() {
            echo "stopped-at-probe=$probe_count"
            exit 0
        }
        watchdog_health_loop
    ) 2>&1
)
expect_contains "health supervision armed after first successful response" "$late_readiness_result" \
    "a later successful health probe arms supervision"
expect_contains "stopped-at-probe=5" "$late_readiness_result" \
    "only failures after late readiness reach restart evidence"
expect_not_contains "stopped-at-probe=2" "$late_readiness_result" \
    "failures before the first success cannot kill the engine"

unarmed_result=$(
    (
        WATCHDOG_PROBE_INTERVAL=0
        WATCHDOG_FAIL_THRESHOLD=2
        WATCHDOG_COOLDOWN=0
        probe_count=0
        curl() {
            probe_count=$((probe_count + 1))
            return 1
        }
        sleep() {
            if [ "$probe_count" -ge 6 ]; then
                echo "unarmed-probes=$probe_count"
                exit 0
            fi
        }
        watchdog_trim_log() { :; }
        wedge_scan() { echo "unexpected-evidence"; }
        watchdog_stop_engine() { echo "unexpected-stop"; exit 1; }
        watchdog_health_loop
    ) 2>&1
)
expect_contains "unarmed-probes=6" "$unarmed_result" \
    "initial health misses do not exit continuous supervision"
expect_not_contains "unexpected-evidence" "$unarmed_result" \
    "unarmed failures do not collect wedge evidence"
expect_not_contains "unexpected-stop" "$unarmed_result" \
    "unarmed failures cannot stop the engine"

if [ "$failures" -ne 0 ]; then
    echo "$failures test(s) failed"
    exit 1
fi
echo "all watchdog tests passed"
