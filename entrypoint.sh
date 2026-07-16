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

# ── Start a system D-Bus + a virtual X display for CEF/Aura ──────────────────
# Both run for the container's whole lifetime (not tied to any one engine-host
# restart). `set -e` is active but neither failure should abort boot — dbus or
# Xvfb already running (a container restart re-using state) or briefly
# unavailable is not fatal, so each step is guarded with `|| true`. `-nolisten
# tcp` keeps the virtual display off the network; `-fork` daemonizes dbus so
# this script continues without waiting on it.
mkdir -p /run/dbus
if [ ! -S /run/dbus/system_bus_socket ]; then
    dbus-daemon --system --fork || echo "entrypoint: WARNING: dbus-daemon failed to start" >&2
fi

Xvfb :99 -screen 0 1280x1024x24 -nolisten tcp &
export DISPLAY=:99

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
        # shellcheck disable=SC2086
        $RUN_AS "$ENGINE_HOST_BIN" || true
        echo "entrypoint: engine-host exited (code $?); restarting in 3s" >&2
        sleep 3
    done
}
supervise_engine &

# Wait for the host's first /health (bounded) so the container reports ready.
i=0
while [ "$i" -lt 60 ]; do
    if curl -fsS "http://127.0.0.1:${ENGINE_PORT}/health" > /dev/null 2>&1; then
        echo "entrypoint: engine-host is up on :${ENGINE_PORT}"
        break
    fi
    i=$((i + 1))
    sleep 2
done
[ "$i" -ge 60 ] && echo "entrypoint: WARNING: engine-host /health did not come up in time" >&2

# ── Hand off to the Go server (PID for signals via tini) ─────────────────────
# shellcheck disable=SC2086
exec $RUN_AS /app/tsundoku "$@"
