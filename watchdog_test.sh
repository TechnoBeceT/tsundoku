#!/bin/sh
# watchdog_test.sh — fixture tests for the wedge predicate.
#
# Sourcing watchdog.sh must be side-effect free: it defines functions and sets
# defaults, and starts nothing. If this script ever hangs, that contract broke.
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

expect_eq "wedged dump counts 8 blocked pool threads" \
    "8" "$(wedge_blocked_count "$FIXTURES/dump-wedged.txt")"
expect_eq "busy dump counts 0 blocked pool threads" \
    "0" "$(wedge_blocked_count "$FIXTURES/dump-busy.txt")"
expect_eq "boundary dump counts exactly 5" \
    "5" "$(wedge_blocked_count "$FIXTURES/dump-5of8.txt")"
expect_eq "truncated dump counts 0 (header with no state line)" \
    "0" "$(wedge_blocked_count "$FIXTURES/dump-truncated.txt")"
expect_eq "contended non-pool threads are not counted" \
    "0" "$(wedge_blocked_count "$FIXTURES/dump-contended.txt")"
expect_eq "missing file counts 0" \
    "0" "$(wedge_blocked_count "$FIXTURES/does-not-exist.txt")"

expect_eq "held monitor is extracted from the wedged dump" \
    "0x0000000084271758 eu.kanade.tachiyomi.extension.en.comix.ExtensionGenerated" \
    "$(wedge_held_monitor "$FIXTURES/dump-wedged.txt")"
expect_eq "held monitor is 'unknown' when nothing is blocked" \
    "unknown" "$(wedge_held_monitor "$FIXTURES/dump-busy.txt")"

if [ "$failures" -ne 0 ]; then
    echo "$failures test(s) failed"
    exit 1
fi
echo "all watchdog tests passed"
