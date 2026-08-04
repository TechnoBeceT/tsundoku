#!/bin/sh
# entrypoint.sh — start the engine-host + the Tsundoku Go server as an
# unprivileged PUID/PGID user.
#
# Suwayomi is gone (P1+P2 complete): this image runs the JVM extension-host
# (engine-host) as the source engine and the Go server alongside it.
#
# XVFB + DBUS ARE REQUIRED (reverted from the earlier "no Xvfb" assumption): the
# engine-host uses KCEF (Chromium) for WebView sources, and CEF's Aura/Ozone
# platform layer fails to initialize without a real or virtual X display and a
# D-Bus session — even with `--off-screen-rendering-enabled --disable-gpu`
# (confirmed live: "Failed to connect to the bus" / "The platform failed to
# initialize" / "ContentMainRun failed with exit code 1", then every
# WebView-gated source times out and 502s). Upstream Suwayomi-Server runs its
# bundled Chromium under Xvfb for the same reason. Both are started below,
# BEFORE the engine-host, and DISPLAY is exported so the engine-host's JVM (and
# its Chromium children) inherit it across the PUID/PGID user switch (gosu
# preserves the environment) and across every supervised restart.
#
# SUPERVISION: this entrypoint owns the engine-host process — a background loop
# restarts it if it dies while the container runs. The Go server does NOT own
# the host process: it is a pure HTTP client that connects to it over
# localhost:7777 via TSUNDOKU_ENGINE_URL (see internal/sourceengine). The Go
# server is the foreground process; when it exits the container stops.
#
# "$@" is forwarded to the Go binary.
set -e

PUID=${PUID:-0}
PGID=${PGID:-0}
ENGINE_HOST_BIN=/app/engine-host/bin/tsundoku-engine-host
ENGINE_DATA=${TSUNDOKU_ENGINE_DATA:-/config/engine}
ENGINE_PORT=${TSUNDOKU_ENGINE_PORT:-7777}

# The wedge watchdog lives in its own file so its predicate can be unit-tested. This
# script starts Xvfb and dbus at load, so a test that sourced THIS file to reach the
# predicate would launch a virtual display.
#
# The path below is the IN-IMAGE one; the directive tells the linter where the same
# file lives in the repository, so `shellcheck -x` can follow it instead of emitting
# SC1091 and failing the container lint job.
# shellcheck source=watchdog.sh
. /app/watchdog.sh

# ── Start a system D-Bus + a supervised virtual X display for CEF/Aura ───────
# Both back the engine-host's Chromium (KCEF) layer for the container's whole
# lifetime. `set -e` is active, so each start is guarded: an already-bound dbus
# socket (a container restart re-using state) or a transient failure must not
# abort boot. `-fork` daemonizes dbus so this script continues without waiting.
mkdir -p /run/dbus
if [ ! -S /run/dbus/system_bus_socket ]; then
    dbus-daemon --system --fork || echo "entrypoint: WARNING: dbus-daemon failed to start" >&2
fi

# The virtual display is SUPERVISED, not fire-and-forget: WebView sources go dark
# the moment Xvfb dies. /tmp lives on the container's writable layer (only /config
# and /series are volumes), so a dead Xvfb leaves a STALE /tmp/.X99-lock + the
# /tmp/.X11-unix/X99 socket behind — the next Xvfb then aborts with "Server is
# already active for display 99" and the display stays dead permanently, even
# across a plain `docker restart` (the writable layer persists). This is the exact
# stale-lock class already cleared for the KCEF singleton below; clean_x_lock does
# it for the X display before every (re)launch. `-nolisten tcp` keeps the display
# off the network.
clean_x_lock() {
    rm -f /tmp/.X99-lock /tmp/.X11-unix/X99 2>/dev/null || true
}

# supervise_display keeps the virtual display alive for the container's lifetime,
# mirroring supervise_engine below. The engine-host binds the display ONCE during
# its CEF init at host boot and will not re-attach to a fresh one, so on any
# restart-after-death — never the FIRST launch, when the host is not up yet — it
# also bounces the engine-host: `pkill -f tsundoku-engine-host` matches the
# launcher and its JVM classpath jar (/app/engine-host/lib/tsundoku-engine-host-*.jar)
# but NOT the Go binary (/app/tsundoku) or Xvfb, so only the source engine is
# cycled; supervise_engine then restarts it against the live display. Every step
# is guarded so a `set -e` abort is impossible on the loop's normal path (a missing
# pkill, a non-matching pkill, or Xvfb's own exit status all fall through `|| true`).
supervise_display() {
    first_launch=1
    while true; do
        clean_x_lock
        Xvfb :99 -screen 0 1280x1024x24 -nolisten tcp &
        display_pid=$!
        if [ "$first_launch" -eq 1 ]; then
            first_launch=0
        else
            # The fresh Xvfb above has NOT necessarily bound its socket yet, and
            # clean_x_lock just removed the old one. Wait (bounded, <=15x1s) for the
            # new display to actually come up BEFORE bouncing the host — otherwise the
            # relaunched engine-host re-inits CEF against a socketless display, fails,
            # and (being log-and-continue on a CEF fault) stays up with a dead display
            # and never retries. Relying on supervise_engine's 3s backoff to outrace
            # Xvfb is a time gamble that loses under the resource starvation that killed
            # Xvfb in the first place. Same safe idiom as the boot-time socket wait; a
            # separate counter ("d") so the two loops never alias.
            d=0
            while [ "$d" -lt 15 ]; do
                if [ -S /tmp/.X11-unix/X99 ]; then
                    break
                fi
                d=$((d + 1))
                sleep 1
            done
            pkill -f tsundoku-engine-host 2>/dev/null || true
        fi
        wait "$display_pid" || true
        echo "entrypoint: virtual display exited; restarting in 2s" >&2
        sleep 2
    done
}
supervise_display &
export DISPLAY=:99

# Bounded wait (<=15 tries, 1s each) for the display socket so the engine-host's
# first CEF init below sees a live display. It NEVER blocks boot — after the cap it
# proceeds with a warning rather than hanging.
i=0
while [ "$i" -lt 15 ]; do
    if [ -S /tmp/.X11-unix/X99 ]; then
        break
    fi
    i=$((i + 1))
    sleep 1
done
[ -S /tmp/.X11-unix/X99 ] || echo "entrypoint: WARNING: virtual display socket did not appear in time" >&2

# ── Seed the BUNDLED KCEF (Chromium) runtime ─────────────────────────────────
# The image bakes a pre-downloaded Chromium at $ENGINE_KCEF_BUNDLE. CEFManager
# looks for it at <dataRoot>/bin/kcef; symlink the bundle there (on the persistent
# /config volume) so there is NO first-run download. Clear any stale Chromium
# singleton lock left over from a previous container (its hostname is dead after a
# recreate, else Chromium refuses to launch and WebView sources time out).
if [ -n "${ENGINE_KCEF_BUNDLE:-}" ] && [ -d "${ENGINE_KCEF_BUNDLE}" ]; then
    mkdir -p "${ENGINE_DATA}/bin"
    if [ ! -e "${ENGINE_DATA}/bin/kcef" ]; then
        ln -sfn "${ENGINE_KCEF_BUNDLE}" "${ENGINE_DATA}/bin/kcef"
    fi
fi
rm -f "${ENGINE_DATA}/cache/kcef/SingletonLock" \
      "${ENGINE_DATA}/cache/kcef/SingletonCookie" \
      "${ENGINE_DATA}/cache/kcef/SingletonSocket" 2>/dev/null || true

# ── Resolve the runtime user (PUID/PGID convention) ──────────────────────────
if [ "$PUID" -eq 0 ] && [ "$PGID" -eq 0 ]; then
    RUN_AS=""            # run everything as root
else
    if ! getent group "$PGID" > /dev/null 2>&1; then
        groupadd -g "$PGID" tsundoku
    fi
    if ! id "$PUID" > /dev/null 2>&1; then
        useradd -u "$PUID" -g "$PGID" -d /app -s /bin/sh -M tsundoku
    fi
    USER_NAME=$(id -nu "$PUID")
    chown "$PUID:$PGID" /config 2>/dev/null || true
    chown "$PUID:$PGID" /series 2>/dev/null || true
    chown -R "$PUID:$PGID" "$ENGINE_DATA" 2>/dev/null || true
    RUN_AS="gosu $USER_NAME"
fi

# ── Supervise the engine-host: restart it if it dies while the container runs ─
# A background loop keeps the host alive (crash / OOM / native KCEF fault). It exits
# only when the loop file is removed, which never happens here — tini tears the loop
# down when the foreground Go server exits (container stop). A short backoff avoids a
# hot crash-loop.
supervise_engine() {
    while true; do
        # Start every (re)launch from clean CEF state. A killed engine-host does NOT
        # dispose CEF: its only shutdown hook stops the RPC server (Main.kt), never
        # CefApp, so the out-of-process Chromium children (cef_server / jcef_helper)
        # reparent to tini and linger, holding the CEF singleton socket. The relaunched
        # host's CEF init would then fail on the held singleton and — being
        # log-and-continue on a CEF fault — stay up with a dead WebView layer forever.
        # (a) Reap those orphans by their KCEF bundle path. That path appears only in
        # the CEF children's argv (CEF passes it as a subprocess/resources arg); the
        # java host receives it programmatically as CEF settings, not on its argv, and
        # neither the Go binary nor Xvfb carries it — so this cannot match them.
        pkill -f "${ENGINE_DATA}/bin/kcef" 2>/dev/null || true
        # (b) Clear the singleton files a dead Chromium left locked — same paths as the
        # boot-time seed block (kept there too: it runs in ordered sequence before this
        # background loop is even started, and other comments cross-reference it).
        rm -f "${ENGINE_DATA}/cache/kcef/SingletonLock" \
              "${ENGINE_DATA}/cache/kcef/SingletonCookie" \
              "${ENGINE_DATA}/cache/kcef/SingletonSocket" 2>/dev/null || true
        # Capture the host's output to a file so its SIGQUIT thread dump is readable
        # by the watchdog (a JVM writes that dump to its own stdout and nowhere else).
        # Truncated per launch so a dump can never be confused with a previous one.
        : > "$WATCHDOG_LOG_FILE"
        # Backgrounded rather than piped: in a `cmd | tee` pipeline `$!` is tee's pid,
        # not the engine's, and SIGQUIT to the wrong process produces no dump. Both
        # gosu and the launcher script exec, so this pid IS the JVM's.
        # shellcheck disable=SC2086
        $RUN_AS "$ENGINE_HOST_BIN" >> "$WATCHDOG_LOG_FILE" 2>&1 &
        engine_pid=$!
        echo "$engine_pid" > "$WATCHDOG_PID_FILE"
        engine_status=0
        wait "$engine_pid" || engine_status=$?
        rm -f "$WATCHDOG_PID_FILE"
        # Reported from a saved status: reading $? after `|| true` always yields 0,
        # which is why this line used to claim every crash exited cleanly.
        echo "entrypoint: engine-host exited (code $engine_status); restarting in 3s" >&2
        sleep 3
    done
}
# `docker logs` must keep showing engine-host output now that it is redirected to a
# file. `tail -F` follows the per-launch truncation correctly.
: > "$WATCHDOG_LOG_FILE"
tail -n +1 -F "$WATCHDOG_LOG_FILE" &
supervise_engine &

# ── Wait for the host's first /health ────────────────────────────────────────
# EVERY attempt MUST be bounded, and `curl` has no default timeout. A wedged
# engine-host still ACCEPTS the TCP connection — its accept loop is not one of the
# blocked request-pool threads — and then never replies, so an unbounded probe blocks
# for as long as the fault lasts. This loop both arms the wedge watchdog and precedes
# the `exec` of the Go server below, so one such probe leaves the container with NO API
# and NO watchdog: the very fault this feature exists to recover would prevent its own
# detection. Observed against a stalled engine: PID 1 still in this script, with a
# 45-second-old curl child (GAP-137).
#
# ENGINE_BOOT_PROBE_TIMEOUT is deliberately larger than the watchdog's 5s and the image
# HEALTHCHECK's 5s. Those probe a WARM engine; this one waits on a cold JVM that is
# loading every installed extension and initialising CEF while serving /health from that
# same request pool, so a steady-state timeout would read a slow-but-healthy boot as
# down and skip arming the watchdog.
#
# The overall wait is a DEADLINE, not a try count, because the two failure shapes cost
# very different amounts per attempt: nothing listening yet is REFUSED instantly (~2s an
# attempt, all of it the sleep), while a bound-but-stalled engine costs up to
# timeout + 2s. No single try count can give the first case enough grace without giving
# the second an unacceptable multiple of it. The count survives only as a backstop, so
# the loop stays bounded even if `date` cannot be read; on the ordinary refused path the
# two agree exactly (60 x 2s = 120s = ENGINE_BOOT_WAIT).
ENGINE_BOOT_PROBE_TIMEOUT=${ENGINE_BOOT_PROBE_TIMEOUT:-10}
ENGINE_BOOT_WAIT=${ENGINE_BOOT_WAIT:-120}

engine_up=0
# Guarded like every substitution in watchdog.sh's loop: `set -e` is active, and an
# unguarded `x=$(cmd)` that fails would abort boot outright.
boot_started=$(date +%s 2>/dev/null || echo 0)
boot_started=${boot_started:-0}
i=0
while [ "$i" -lt 60 ]; do
    if curl -fsS --max-time "$ENGINE_BOOT_PROBE_TIMEOUT" "http://127.0.0.1:${ENGINE_PORT}/health" > /dev/null 2>&1; then
        echo "entrypoint: engine-host is up on :${ENGINE_PORT}"
        engine_up=1
        break
    fi
    i=$((i + 1))
    now=$(date +%s 2>/dev/null || echo 0)
    now=${now:-0}
    if [ "$now" -gt 0 ] && [ $((now - boot_started)) -ge "$ENGINE_BOOT_WAIT" ]; then
        break
    fi
    sleep 2
done

# The wedge watchdog is armed ONLY inside the success branch, so a slow first boot
# (CEF init can take a while) can never be mistaken for a wedge. That promise used to
# be a comment only: the wait above fell through after the cap with nothing but a
# warning and started the watchdog regardless.
#
# Both outcomes are logged, because the failure mode of a watchdog is SILENCE and the
# two silences are indistinguishable otherwise: "armed, nothing has wedged" and "never
# armed at all" produce exactly the same empty log. An operator greps for one of these
# two lines to tell them apart.
if [ "$engine_up" -eq 1 ]; then
    supervise_engine_health &
    echo "entrypoint: wedge watchdog armed (GAP-137)"
else
    echo "entrypoint: WARNING: engine-host /health did not answer within ~${ENGINE_BOOT_WAIT}s; the wedge watchdog was NOT started (GAP-137). supervise_engine still restarts the host if it DIES, but a deadlock will not be detected or recovered until the container is restarted." >&2
fi

# ── Hand off to the Go server (PID for signals via tini) ─────────────────────
# shellcheck disable=SC2086
exec $RUN_AS /app/tsundoku "$@"
