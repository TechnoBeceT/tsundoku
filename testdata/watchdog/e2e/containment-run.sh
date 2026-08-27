#!/bin/sh
# Focused recovery proof: the default-engine watchdog misses its readiness
# window, later arms, then requires stable source-pool exhaustion before one
# restart and suppresses a repeat during cooldown.
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

WATCHDOG_E2E_BOOT_WAIT=${WATCHDOG_E2E_BOOT_WAIT:-4} \
WATCHDOG_E2E_LATE_READY_MS=${WATCHDOG_E2E_LATE_READY_MS:-8000} \
WATCHDOG_E2E_COOLDOWN=${WATCHDOG_E2E_COOLDOWN:-60} \
    sh "$SCRIPT_DIR/run.sh" containment
