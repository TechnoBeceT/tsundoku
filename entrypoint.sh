#!/bin/sh
# entrypoint.sh — run Tsundoku as an unprivileged PUID/PGID user.
#
# Self-hosted stacks (LXC, unRAID, Synology, plain docker-compose) expect
# containers to write files owned by a specific host user so that mounted
# volumes stay accessible from the host. This script honours the conventional
# PUID/PGID environment variables:
#
#   - If both PUID and PGID are 0, we're meant to run as root: exec directly.
#   - Otherwise we create a matching group + user, fix ownership on the data
#     volumes, and drop to that user with gosu before starting the server.
#
# "$@" is forwarded so any extra command arguments pass through to the binary.
set -e

PUID=${PUID:-0}
PGID=${PGID:-0}

# ── Virtual X display for embedded Suwayomi's WebView (KCEF/Chromium) ──────────
# KCEF requires a real X DISPLAY even when running headless. Start Xvfb on :99 and
# export DISPLAY so the embedded Suwayomi Java process — spawned by the Go binary,
# which inherits this environment (process.go does not override cmd.Env) — can
# start a WebView for JS/challenge sources. Flags:
#   -ac          disable X access control, so a dropped-privilege (PUID != 0)
#                client can still connect to this root-owned display.
#   -nolisten tcp  no network X socket; local unix socket only (/tmp/.X11-unix).
# Backgrounded: when this script later exec's the server, Xvfb reparents to tini
# (PID 1) and keeps running. Xvfb starts in well under a second; a source's first
# WebView use happens minutes later, so no readiness wait is needed.
#
# Skip it entirely when:
#   - TSUNDOKU_SUWAYOMI_EXTERNALURL is set (EXTERNAL mode → no embedded Java, no
#     WebView here — the external Suwayomi host owns its own display), or
#   - a DISPLAY is already provided (respect an operator-supplied X server), or
#   - Xvfb isn't installed (a slim/dev image — WebView-only sources just won't work).
if [ -z "${TSUNDOKU_SUWAYOMI_EXTERNALURL:-}" ] && [ -z "${DISPLAY:-}" ] && \
   command -v Xvfb > /dev/null 2>&1; then
    # A `docker restart` reuses the container's writable layer, so /tmp still holds
    # the X lock + socket from the PREVIOUS run. `Xvfb :99` then aborts with "Server
    # is already active for display 99" and exits — yet DISPLAY=:99 is exported
    # below regardless, so the WebView dials a dead display and the source fails
    # with "Can't connect to X11 window". Clear the stale lock/socket first so Xvfb
    # can always re-acquire :99 on a restart (harmless on a fresh container).
    rm -f /tmp/.X99-lock /tmp/.X11-unix/X99 2>/dev/null || true
    Xvfb :99 -screen 0 1280x1024x24 -ac -nolisten tcp > /dev/null 2>&1 &
    export DISPLAY=:99
    # The line above backgrounds Xvfb and can't observe its exit, so wait briefly
    # for the display socket to appear. This turns a silent Xvfb failure into a
    # visible startup warning instead of a confusing WebView connect error later.
    i=0
    while [ "$i" -lt 50 ]; do
        if [ -S /tmp/.X11-unix/X99 ]; then
            break
        fi
        i=$((i + 1))
        sleep 0.1
    done
    if [ ! -S /tmp/.X11-unix/X99 ]; then
        echo "entrypoint: WARNING: Xvfb display :99 did not come up; WebView sources (e.g. Comix) will fail" >&2
    fi
fi

# ── Clear the stale KCEF (WebView Chromium) profile lock ──────────────────────
# Suwayomi's WebView engine (KCEF) keeps its Chromium profile under the PERSISTENT
# /config volume (<runtimedir>/cache/kcef). Chromium writes a `SingletonLock`
# symlink naming the HOSTNAME + PID that owns the profile. Container hostnames are
# random and change on every recreate, so after any restart/recreate the lock
# points at a dead hostname; Chromium then refuses to launch ("The profile appears
# to be in use by another Chromium process on another computer", ContentMainRun
# exit code 21). Only the zygote survives, no browser starts, and WebView sources
# (e.g. Comix) fail with "Timed out starting WebView". Unlike the X11 lock this
# one lives in a mounted volume, so it survives even a full image update — it must
# be cleared on every boot. No Chromium is running yet at container start, so
# removing the singleton files is safe (Chromium recreates them on launch).
# EMBEDDED mode only: in external mode no local KCEF exists.
if [ -z "${TSUNDOKU_SUWAYOMI_EXTERNALURL:-}" ]; then
    KCEF_PROFILE="${TSUNDOKU_SUWAYOMI_RUNTIMEDIR:-/config/suwayomi}/cache/kcef"
    rm -f "$KCEF_PROFILE/SingletonLock" \
          "$KCEF_PROFILE/SingletonCookie" \
          "$KCEF_PROFILE/SingletonSocket" 2>/dev/null || true
fi

# Run as root when explicitly asked (both IDs 0) — no user juggling needed.
if [ "$PUID" -eq 0 ] && [ "$PGID" -eq 0 ]; then
    exec /app/tsundoku "$@"
fi

# Create the group if a group with this GID doesn't already exist.
if ! getent group "$PGID" > /dev/null 2>&1; then
    groupadd -g "$PGID" tsundoku
fi

# Create the user if a user with this UID doesn't already exist.
if ! id "$PUID" > /dev/null 2>&1; then
    useradd -u "$PUID" -g "$PGID" -d /app -s /bin/sh -M tsundoku
fi
USER_NAME=$(id -nu "$PUID")

# Best-effort: make the mounted data volumes writable by the runtime user.
# The "|| true" keeps startup resilient if a volume is read-only or the chown
# is otherwise not permitted — the server will surface any real access error.
chown "$PUID:$PGID" /config 2>/dev/null || true
chown "$PUID:$PGID" /series 2>/dev/null || true

# Drop privileges and hand off to the server.
exec gosu "$USER_NAME" /app/tsundoku "$@"
