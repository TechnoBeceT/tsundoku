#!/bin/sh
# run.sh — end-to-end test for the engine-host wedge watchdog (GAP-137).
#
#   sh testdata/watchdog/e2e/run.sh
#
# Requires Docker and nothing else. Takes roughly three to five minutes, most of it the one-off
# image build; re-runs against a warm layer cache are much faster.
#
# ── Why this exists ──────────────────────────────────────────────────────────────────────────
# watchdog_test.sh covers the PREDICATE (wedge_scan / wedge_verdict) against checked-in dumps. It
# cannot cover the LOOP — supervise_engine_health, the thing that actually decides to kill the
# production source engine. Both high-severity defects this branch fixed were found by driving the
# loop against a live JVM, not by the fixture suite:
#
#   * a majority "6 of 8 blocked" rule cannot tell a deadlock from a busy engine, and killed a
#     healthy, progressing one;
#   * the pool-thread name the predicate anchors on comes from a process-global JDK counter, so an
#     unrelated pool created first silently blinded the watchdog to a genuine permanent deadlock.
#
# Each case below boots the real entrypoint.sh and watchdog.sh in a container, wedges or saturates a
# real JVM, and asserts what the watchdog DID.
#
# ── The cases ────────────────────────────────────────────────────────────────────────────────
#   wedge-named    a true deadlock, pool threads named engine-http-* (the current front door)
#                  -> WEDGE CONFIRMED -> the engine is restarted -> /health answers again
#   wedge-jdk      the same deadlock with JDK-default pool-<n>-thread-<m> names, and the RPC pool
#                  deliberately NOT pool-1 -> the fallback path must reach the same verdict. This is
#                  the rollback shape: an image built before the thread factory was named.
#   busy           a legitimate long call holding the monitor from INSIDE the pool
#                  -> SATURATED verdict -> the engine is NOT killed -> the long call completes
#   boot-wedge     the same deadlock, but wedged the instant the listener binds — BEFORE the
#                  entrypoint's boot health probe has ever succeeded -> the probe must time out per
#                  attempt, give up on its deadline, leave supervision unarmed and waiting, and
#                  still hand off to the foreground server
#
# The busy case is the important one. It is the regression test for the rule that killed a healthy
# engine, and it asserts three separable things: the verdict, that the process was never restarted
# (same pid), and that the work finished (the engine's own completed-call counter went up).
#
# boot-wedge covers the one ordering the other three cannot reach, because each of them waits for
# the watchdog to be armed first. A wedged engine ACCEPTS the connection (the accept loop is not a
# pool thread) and then never replies, so an unbounded boot probe blocks for as long as the fault
# lasts — leaving the container with no API and no watchdog, and the fault undetectable by the very
# feature written to detect it. Observed in production before the fix: PID 1 still in entrypoint.sh
# with a 45-second-old curl child.
#
# ── Proving the assertions can fail ──────────────────────────────────────────────────────────
# An e2e test that cannot fail is worse than none, so the two defects are re-injectable. The
# injection rewrites the COPY of watchdog.sh inside the throwaway build context; the tracked file is
# never touched, and each rewrite is verified to have applied before anything is built.
#
#   WATCHDOG_E2E_BREAK=majority sh testdata/watchdog/e2e/run.sh busy
#       restores the "blocked >= 6" majority rule. The busy case must FAIL: the healthy engine is
#       killed. That is the defect, reproduced.
#
#   WATCHDOG_E2E_BREAK=pool1 sh testdata/watchdog/e2e/run.sh wedge-jdk
#       re-anchors the fallback to pool-1 only. The wedge-jdk case must FAIL: no pool threads are
#       found and the deadlocked engine is left running forever.
#
#   WATCHDOG_E2E_BREAK=bootprobe sh testdata/watchdog/e2e/run.sh boot-wedge
#       removes --max-time from the entrypoint's boot health probe. The boot-wedge case must FAIL:
#       the single curl never returns, the entrypoint never reaches its exec, and the container
#       serves nothing at all. This break targets entrypoint.sh, not watchdog.sh.
#
# CI never sets WATCHDOG_E2E_BREAK. A run with it set prints a loud banner and is not a gate.
#
# ── Other knobs ──────────────────────────────────────────────────────────────────────────────
#   WATCHDOG_E2E_IMAGE  image tag to build/use (default tsundoku-watchdog-e2e:local)
#   WATCHDOG_E2E_KEEP   set to 1 to leave containers running for inspection
#   $1..                cases to run; default is all four
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH='' cd -- "$SCRIPT_DIR/../../.." && pwd)

IMAGE=${WATCHDOG_E2E_IMAGE:-tsundoku-watchdog-e2e:local}
BREAK=${WATCHDOG_E2E_BREAK:-none}
KEEP=${WATCHDOG_E2E_KEEP:-0}

# The watchdog's shipped cadence is minutes (30s probes, a 600s cooldown). Compressed here so a case
# completes in under a minute. Only the TIMING is compressed — the predicate, the verdicts and the
# kill path are the shipped ones.
E2E_PROBE_INTERVAL=2
E2E_PROBE_TIMEOUT=2
E2E_FAIL_THRESHOLD=2
E2E_DUMP_SETTLE=3
E2E_COOLDOWN=30
E2E_TERM_GRACE=5
# The legitimate long call must outlast the watchdog's whole decision path (a few probes, then the
# dump settle) so the verdict is reached while the call is still running — otherwise "not killed"
# would be true only because there was nothing left to kill.
E2E_BUSY_HOLD_MS=45000

# The entrypoint's own boot health probe, compressed the same way. The DEADLINE must stay
# comfortably above the time this fake JVM takes to bind (about a second) or the three healthy
# cases would fall through it and never arm the watchdog; 30s is that margin, and it is also how
# long the boot-wedge case takes to give up.
E2E_BOOT_PROBE_TIMEOUT=3
E2E_BOOT_WAIT=30

# Budgets, in seconds. Generous: a cold container start on a loaded CI runner is not fast, and a
# timeout here is reported as a failure with the container log attached rather than as a hang.
BUDGET_BOOT=90
BUDGET_VERDICT=90
BUDGET_RECOVER=120

failures=0
containers=''

log()  { echo "e2e: $1"; }
pass() { echo "ok   - $1"; }
fail() { echo "FAIL - $1"; failures=$((failures + 1)); }
die()  { echo "e2e: $1" >&2; exit 2; }

cleanup() {
    [ -n "$containers" ] || return 0
    if [ "$KEEP" = "1" ]; then
        echo "e2e: WATCHDOG_E2E_KEEP=1, leaving containers: $containers" >&2
        return 0
    fi
    for c in $containers; do
        docker rm -f "$c" >/dev/null 2>&1 || true
    done
}
trap cleanup EXIT INT TERM

# ── Build ────────────────────────────────────────────────────────────────────────────────────

# apply_break CTX — re-inject one of the three defects into the build context's copies of
# watchdog.sh / entrypoint.sh. Each branch names the file it targets, because they are not all the
# same file, and verifies its own substitution landed: a sed that quietly matched nothing would turn
# "the test failed as expected" into a lie, which is the exact failure mode this whole section
# exists to rule out.
apply_break() {
    case "$BREAK" in
        none)
            return 0
            ;;
        majority)
            # The shipped rule is `blocked == seen`. This restores the majority threshold that could
            # not distinguish a deadlock from saturation.
            _f=$1/watchdog.sh
            # shellcheck disable=SC2016  # the $v_* here are watchdog.sh's variable NAMES, matched literally
            sed 's/\[ "$v_blocked" -eq "$v_seen" \]/[ "$v_blocked" -ge 6 ]/' "$_f" > "$_f.broken"
            grep -q -- '-ge 6' "$_f.broken" || die "break 'majority' did not apply — the verdict rule has moved"
            ;;
        pool1)
            # The shipped fallback matches any pool number. This re-anchors it to pool-1, which the
            # RPC pool only ever was by coincidence.
            _f=$1/watchdog.sh
            sed 's/"pool-\[0-9\]+-thread/"pool-1-thread/' "$_f" > "$_f.broken"
            grep -q '"pool-1-thread' "$_f.broken" || die "break 'pool1' did not apply — the fallback pattern has moved"
            ;;
        bootprobe)
            # The shipped boot probe bounds every attempt. This removes the bound, restoring the
            # curl that blocks for as long as a wedged engine holds its accepted connection open.
            _f=$1/entrypoint.sh
            # shellcheck disable=SC2016  # matching the literal variable NAME in entrypoint.sh
            sed 's/curl -fsS --max-time "$ENGINE_BOOT_PROBE_TIMEOUT" "http:\/\/127/curl -fsS "http:\/\/127/' "$_f" > "$_f.broken"
            grep -q 'curl -fsS "http://127' "$_f.broken" || die "break 'bootprobe' did not apply — the boot probe has moved"
            ;;
        *)
            die "unknown WATCHDOG_E2E_BREAK: $BREAK (expected none, majority, pool1 or bootprobe)"
            ;;
    esac
    mv "$_f.broken" "$_f"
    echo "e2e: ###########################################################################" >&2
    echo "e2e: # FAULT INJECTED (WATCHDOG_E2E_BREAK=$BREAK). This run is NOT a gate."       >&2
    echo "e2e: # It exists to prove the assertions below can fail. Expect a FAIL."          >&2
    echo "e2e: ###########################################################################" >&2
}

build_image() {
    _ctx=$(mktemp -d) || die "cannot create a build context"
    cp "$SCRIPT_DIR/Dockerfile" "$SCRIPT_DIR/FakeEngine.java" "$_ctx/"
    cp "$REPO_ROOT/entrypoint.sh" "$REPO_ROOT/watchdog.sh" "$_ctx/"
    apply_break "$_ctx"
    log "building $IMAGE"
    if ! docker build -q -t "$IMAGE" "$_ctx" >/dev/null; then
        rm -rf "$_ctx"
        die "image build failed"
    fi
    rm -rf "$_ctx"
}

# ── Container helpers ────────────────────────────────────────────────────────────────────────

# start_engine CONTAINER_NAME THREAD_NAMES [WEDGE_AT_BOOT] [EXHAUST_AT_BOOT]
#
# WEDGE_AT_BOOT defaults to 0, so the three cases that want a HEALTHY engine to start with are
# unchanged and do not have to pass it.
#
# The caller passes the name and registers it for cleanup ITSELF. This function must not do the
# registering, because the obvious shape — `cid=$(start_engine …)` with the name echoed out — runs
# the whole function in a command-substitution SUBSHELL, so any `containers=…` it performed is
# discarded when that subshell exits and the EXIT trap then finds nothing to remove. Every container
# from every run is left behind, still running.
start_engine() {
    _name=$1
    docker rm -f "$_name" >/dev/null 2>&1 || true
    docker run -d --name "$_name" \
        -e "FAKE_ENGINE_THREAD_NAMES=$2" \
        -e "FAKE_WEDGE_AT_BOOT=${3:-0}" \
        -e "FAKE_EXHAUST_AT_BOOT=${4:-0}" \
        -e "FAKE_BUSY_HOLD_MS=$E2E_BUSY_HOLD_MS" \
        -e "ENGINE_BOOT_PROBE_TIMEOUT=$E2E_BOOT_PROBE_TIMEOUT" \
        -e "ENGINE_BOOT_WAIT=$E2E_BOOT_WAIT" \
        -e "WATCHDOG_PROBE_INTERVAL=$E2E_PROBE_INTERVAL" \
        -e "WATCHDOG_PROBE_TIMEOUT=$E2E_PROBE_TIMEOUT" \
        -e "WATCHDOG_FAIL_THRESHOLD=$E2E_FAIL_THRESHOLD" \
        -e "WATCHDOG_DUMP_SETTLE=$E2E_DUMP_SETTLE" \
        -e "WATCHDOG_COOLDOWN=$E2E_COOLDOWN" \
        -e "WATCHDOG_TERM_GRACE=$E2E_TERM_GRACE" \
        "$IMAGE" >/dev/null
}

# register CASE — mint this case's container name and add it to the cleanup list, in the CURRENT
# shell. See start_engine for why this cannot live inside it.
register() {
    _cid="tsundoku-watchdog-e2e-$$-$1"
    containers="$containers $_cid"
}

# health CID — the /health body, or empty if the engine is not answering. Run from INSIDE the
# container so no host port has to be allocated; --max-time matters because a wedged engine accepts
# the connection (the accept loop is not a pool thread) and then never replies.
health() {
    docker exec "$1" curl -fsS --max-time 3 http://127.0.0.1:7777/health 2>/dev/null || true
}

wait_health() {  # wait_health CID BUDGET -> prints the body, or fails
    _cid=$1; _left=$2
    while [ "$_left" -gt 0 ]; do
        _body=$(health "$_cid")
        if [ -n "$_body" ]; then
            printf '%s' "$_body"
            return 0
        fi
        _left=$((_left - 2))
        sleep 2
    done
    return 1
}

# field BODY NAME — pull one integer out of the fake engine's /health JSON.
field() {
    printf '%s' "$1" | sed -n "s/.*\"$2\":\([0-9][0-9]*\).*/\1/p"
}

# wait_verdict CID BUDGET — block until the watchdog has logged a decision, and name it. Waiting for
# ANY of the four verdicts rather than the expected one is what makes a broken watchdog fail here
# quickly and legibly instead of timing out with no explanation.
wait_verdict() {
    _cid=$1; _left=$2
    while [ "$_left" -gt 0 ]; do
        _out=$(docker logs "$_cid" 2>&1 || true)
        case "$_out" in
            *"WEDGE CONFIRMED"*)                echo wedge;     return 0 ;;
            *"SATURATED, not deadlocked"*)      echo saturated; return 0 ;;
            *"no RPC pool threads found"*)      echo none-seen; return 0 ;;
            *"the dump looks truncated"*)       echo too-few;   return 0 ;;
        esac
        _left=$((_left - 2))
        sleep 2
    done
    echo timeout
}

# log_has CID PATTERN
log_has() {
    docker logs "$1" 2>&1 | grep -q -- "$2"
}

# wait_log CID PATTERN BUDGET — poll the container log for a line.
#
# Every case that asserts a VERDICT must wait for "wedge watchdog armed" before injecting its
# fault, and that is not politeness — it is correctness. The watchdog is armed only once the
# entrypoint's boot health probe has succeeded, so a fault injected before that is judged by a
# watchdog that does not exist yet and every assertion about what it did is vacuous.
#
# The boot probe now bounds each attempt and gives up on a deadline, so injecting first no longer
# HANGS the container. Supervision remains alive but unarmed until a response succeeds; that outcome
# is a case in its own right, see case_boot_wedge.
wait_log() {
    _left=$3
    while [ "$_left" -gt 0 ]; do
        if log_has "$1" "$2"; then
            return 0
        fi
        _left=$((_left - 2))
        sleep 2
    done
    return 1
}

# dump_container_log CID — everything a failure needs: the watchdog's own decisions (the only lines
# that matter, and they are buried under megabytes of thread dump in `docker logs`), the tail of the
# raw log, and the process table. The process table is there because the two ways this can fail
# silently — the watchdog loop never started, or the entrypoint is still blocked in its boot probe —
# are invisible in the log and obvious in `ps`.
dump_container_log() {
    echo "---- watchdog decisions: $1 ----" >&2
    docker logs "$1" 2>&1 | grep -a "^watchdog:" >&2 || echo "(the watchdog logged nothing at all)" >&2
    echo "---- container log tail ----" >&2
    docker logs "$1" 2>&1 | grep -av "^	" | tail -n 20 >&2
    echo "---- processes ----" >&2
    docker exec "$1" ps -ef >&2 2>/dev/null || true
    echo "---- end ----" >&2
}

# ── Cases ────────────────────────────────────────────────────────────────────────────────────

# A true deadlock must be confirmed and recovered. Parameterised over the thread-naming shape
# because the watchdog has two accepted ones and only the authoritative one is exercised by the
# image as it ships — the fallback exists precisely for a rollback, which nothing else covers.
case_wedge() {  # case_wedge LABEL THREAD_NAMES
    _label=$1
    log "case $_label: a true deadlock must be confirmed and recovered"
    register "$_label"
    start_engine "$_cid" "$2"

    if ! _body=$(wait_health "$_cid" "$BUDGET_BOOT"); then
        dump_container_log "$_cid"
        fail "$_label: the engine never became healthy"
        return 0
    fi
    _pid_before=$(field "$_body" pid)
    log "$_label: engine up, pid $_pid_before"

    if ! wait_log "$_cid" "wedge watchdog armed" 30; then
        dump_container_log "$_cid"
        fail "$_label: the watchdog was not armed"
        return 0
    fi

    docker exec "$_cid" curl -fsS --max-time 5 http://127.0.0.1:7777/wedge >/dev/null 2>&1 || true
    log "$_label: engine wedged; waiting for a verdict"

    _verdict=$(wait_verdict "$_cid" "$BUDGET_VERDICT")
    if [ "$_verdict" = "wedge" ]; then
        pass "$_label: the watchdog confirmed the wedge"
    else
        dump_container_log "$_cid"
        fail "$_label: expected verdict 'wedge', got '$_verdict' — a deadlocked engine was left running"
        return 0
    fi

    if log_has "$_cid" "stopping engine-host pid"; then
        pass "$_label: the watchdog stopped the engine"
    else
        dump_container_log "$_cid"
        fail "$_label: the wedge was confirmed but the engine was never stopped"
        return 0
    fi

    if ! _body=$(wait_health "$_cid" "$BUDGET_RECOVER"); then
        dump_container_log "$_cid"
        fail "$_label: /health never came back after the restart"
        return 0
    fi
    _pid_after=$(field "$_body" pid)
    if [ -n "$_pid_after" ] && [ "$_pid_after" != "$_pid_before" ]; then
        pass "$_label: /health answers again from a NEW engine process ($_pid_before -> $_pid_after)"
    else
        fail "$_label: expected a new engine pid, got '$_pid_after' (was '$_pid_before')"
    fi
}

# The regression test for the rule that killed a healthy engine. Three separable assertions, because
# "the engine is still up" alone would also be true of an engine that was killed and restarted.
case_busy() {
    log "case busy: a saturated engine must be left alone"
    register busy
    start_engine "$_cid" engine-http

    if ! _body=$(wait_health "$_cid" "$BUDGET_BOOT"); then
        dump_container_log "$_cid"
        fail "busy: the engine never became healthy"
        return 0
    fi
    _pid_before=$(field "$_body" pid)
    _done_before=$(field "$_body" busyDone)
    log "busy: engine up, pid $_pid_before, completed long calls $_done_before"

    if ! wait_log "$_cid" "wedge watchdog armed" 30; then
        dump_container_log "$_cid"
        fail "busy: the watchdog was not armed"
        return 0
    fi

    docker exec "$_cid" curl -fsS --max-time 5 http://127.0.0.1:7777/busy >/dev/null 2>&1 || true
    log "busy: long call started; waiting for a verdict"

    _verdict=$(wait_verdict "$_cid" "$BUDGET_VERDICT")
    if [ "$_verdict" = "saturated" ]; then
        pass "busy: the watchdog read the engine as saturated, not deadlocked"
    else
        dump_container_log "$_cid"
        fail "busy: expected verdict 'saturated', got '$_verdict'"
    fi

    if log_has "$_cid" "stopping engine-host pid"; then
        dump_container_log "$_cid"
        fail "busy: the watchdog KILLED a healthy engine mid-call"
    else
        pass "busy: the engine was not stopped"
    fi

    if ! _body=$(wait_health "$_cid" "$BUDGET_RECOVER"); then
        dump_container_log "$_cid"
        fail "busy: /health never came back after the long call"
        return 0
    fi
    _pid_after=$(field "$_body" pid)
    _done_after=$(field "$_body" busyDone)

    if [ "$_pid_after" = "$_pid_before" ]; then
        pass "busy: the same engine process is still serving (pid $_pid_after)"
    else
        fail "busy: the engine was restarted ($_pid_before -> $_pid_after)"
    fi

    if [ "$_done_after" = "$((_done_before + 1))" ]; then
        pass "busy: the long call ran to completion (completed calls $_done_before -> $_done_after)"
    else
        fail "busy: the long call did not complete (completed calls $_done_before -> '$_done_after')"
    fi
}

# The regression test for the unbounded boot health probe. The engine is wedged before it has ever
# answered, so every attempt of the entrypoint's boot probe hits a connection that is accepted and
# then held open forever. Bounded, each attempt costs at most its timeout and the loop gives up on
# its deadline; unbounded, the FIRST such attempt never returns and the entrypoint never reaches the
# `exec` of the foreground server.
#
# The assertions are deliberately about BOOT, not about a verdict: with no successful /health the
# watchdog is correctly never armed, and what has to be proven is that the container came up anyway
# and said so.
case_boot_wedge() {
    log "case boot-wedge: an engine that stalls before the first health poll must not block boot"
    register boot-wedge
    start_engine "$_cid" engine-http 1

    # Nothing here waits for health — there will never be any. The budget covers the container's
    # cold start plus the whole compressed boot deadline, with room to spare; a hang shows up as
    # this timing out rather than as the script blocking.
    if wait_log "$_cid" "supervision waiting for first healthy response" $((E2E_BOOT_WAIT + 60)); then
        pass "boot-wedge: the boot probe gave up while supervision stayed unarmed and waiting"
    else
        dump_container_log "$_cid"
        fail "boot-wedge: the entrypoint never got past its boot health probe"
        return 0
    fi

    # THE assertion. This line comes from the foreground stand-in the entrypoint execs last, so it
    # can only appear if the boot probe returned. With the defect present the container reaches
    # nothing after the probe and serves nothing at all.
    if wait_log "$_cid" "foreground stand-in up" 30; then
        pass "boot-wedge: boot proceeded and the foreground server was started"
    else
        dump_container_log "$_cid"
        fail "boot-wedge: the foreground server was never started — the container serves nothing"
        return 0
    fi

    if log_has "$_cid" "stopping engine-host pid"; then
        dump_container_log "$_cid"
        fail "boot-wedge: unarmed supervision stopped an engine before its first healthy response"
    else
        pass "boot-wedge: failures before first readiness did not trigger recovery"
    fi

    # The engine really is still wedged, so the case proved what it claims rather than passing
    # because the fault failed to take hold.
    if [ -z "$(health "$_cid")" ]; then
        pass "boot-wedge: the engine is still wedged (as intended) — boot did not depend on it"
    else
        fail "boot-wedge: the engine answered /health; the boot-time wedge did not take hold"
    fi
}

# A responsive front door with all eight physical source workers stuck must require
# six stable status samples plus matching first/sixth dumps. The fake re-enters the
# same state after restart so the compressed cooldown is also exercised.
case_exhaustion() {
    log "case exhaustion: stable source exhaustion must restart once and respect cooldown"
    register exhaustion
    start_engine "$_cid" engine-http 0 1

    if ! _body=$(wait_health "$_cid" "$BUDGET_BOOT"); then
        dump_container_log "$_cid"
        fail "exhaustion: the responsive control plane never became healthy"
        return 0
    fi
    _pid_before=$(field "$_body" pid)

    if ! wait_log "$_cid" "SOURCE EXHAUSTION CONFIRMED" "$BUDGET_VERDICT"; then
        dump_container_log "$_cid"
        fail "exhaustion: six stable samples did not confirm exhaustion"
        return 0
    fi
    _stops=$(docker logs "$_cid" 2>&1 | grep -c "stopping engine-host pid" || true)
    if [ "$_stops" -eq 1 ]; then
        pass "exhaustion: stable exhaustion stopped the engine exactly once at sample six"
    else
        fail "exhaustion: expected one stop after the first proof, got $_stops"
    fi

    if ! _body=$(wait_health "$_cid" "$BUDGET_RECOVER"); then
        dump_container_log "$_cid"
        fail "exhaustion: /health did not recover after restart"
        return 0
    fi
    _pid_after=$(field "$_body" pid)
    if [ -n "$_pid_after" ] && [ "$_pid_after" != "$_pid_before" ]; then
        pass "exhaustion: recovery launched a new engine process ($_pid_before -> $_pid_after)"
    else
        fail "exhaustion: expected a new engine pid, got '$_pid_after'"
    fi

    _diag=/tmp/engine-host.watchdog-diagnostic
    _status_bytes=$(docker exec "$_cid" sh -c "sed -n 's/^status=//p' $_diag | wc -c" 2>/dev/null || echo 999999)
    _thread_bytes=$(docker exec "$_cid" sh -c "sed -n '/^first_dump=/p;/^sixth_dump=/p' $_diag | wc -c" 2>/dev/null || echo 999999)
    _diag_body=$(docker exec "$_cid" cat "$_diag" 2>/dev/null || true)
    if [ "${_status_bytes:-999999}" -le 32768 ] && [ "${_thread_bytes:-999999}" -le 262144 ]; then
        pass "exhaustion: diagnostics obey status and thread-excerpt caps"
    else
        fail "exhaustion: diagnostic caps exceeded (status=$_status_bytes threads=$_thread_bytes)"
    fi
    case "$_diag_body" in
        *"engine_pid="*"first_dump=engine-source-1"*"sixth_dump=engine-source-1"*)
            pass "exhaustion: diagnostics contain only bounded operational evidence"
            ;;
        *) fail "exhaustion: diagnostics are missing the approved evidence fields" ;;
    esac
    case "$_diag_body" in
        *token*|*cookie*|*header*|*http://*|*https://*)
            fail "exhaustion: diagnostics leaked an unapproved field"
            ;;
        *) pass "exhaustion: diagnostics contain no payload, URL, header, cookie, or token" ;;
    esac

    # A restarted fake exhausts again immediately. Its second six-sample proof lands
    # inside the 30s test cooldown and must not produce another stop.
    sleep 20
    _stops=$(docker logs "$_cid" 2>&1 | grep -c "stopping engine-host pid" || true)
    if [ "$_stops" -eq 1 ] && log_has "$_cid" "holding off (thrash guard)"; then
        pass "exhaustion: cooldown suppressed the repeated restart"
    else
        dump_container_log "$_cid"
        fail "exhaustion: cooldown did not suppress the repeated restart (stops=$_stops)"
    fi
}

# ── Main ─────────────────────────────────────────────────────────────────────────────────────

command -v docker >/dev/null 2>&1 || die "docker is required"
docker info >/dev/null 2>&1 || die "docker is not usable by this user"

cases=${*:-'wedge-named wedge-jdk busy boot-wedge exhaustion'}
build_image

for c in $cases; do
    case "$c" in
        wedge-named) case_wedge wedge-named engine-http ;;
        wedge-jdk)   case_wedge wedge-jdk jdk-default ;;
        busy)        case_busy ;;
        boot-wedge)  case_boot_wedge ;;
        exhaustion)  case_exhaustion ;;
        *)           die "unknown case: $c (expected wedge-named, wedge-jdk, busy, boot-wedge or exhaustion)" ;;
    esac
done

if [ "$failures" -ne 0 ]; then
    echo "$failures assertion(s) failed"
    exit 1
fi
echo "all watchdog e2e cases passed"
