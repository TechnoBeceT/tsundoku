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
