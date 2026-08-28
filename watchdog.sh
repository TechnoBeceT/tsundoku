#!/bin/sh
# watchdog.sh — recover proven engine-host deadlocks and sustained source exhaustion.
#
# Current images isolate the HTTP front door, source scheduler, extension mutations,
# deadlines, and extension networking. Legacy images used one fixed eight-thread RPC
# pool, and both layouts can still be observed during rollout. A non-cooperative
# extension can permanently occupy an execution domain; only process recovery clears
# monitor deadlock after bounded evidence proves it.
#
# So "/health is silent" means "the engine cannot serve requests". It does NOT mean
# "the engine is deadlocked": a single legitimate long call (a large series' chapter
# walk runs ~20 minutes) starves the pool identically. Killing on a timeout alone
# would destroy exactly the expensive work this system tries hardest not to repeat.
#
# Recovery has two independent proofs. Health silence uses the all-BLOCKED predicate
# below. A responsive health plane with all eight source workers occupied uses six
# unchanged /status samples plus matching first/sixth source-worker dumps. Neither
# path treats queue depth or elapsed time alone as a reason to restart.
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
# It is expressed against the threads actually SEEN in one domain, never a literal 8,
# so resizing a pool cannot silently turn it back into a majority rule.
#
# Accepted cost, stated rather than hidden: a wedge whose monitor holder is itself a
# pool thread leaves seen-1 blocked and will NOT fire. The verdict is logged either
# way, so that case is visible in the container log instead of being a mystery.
#
# ── Identifying execution domains ───────────────────────────────────────────
# Three accepted thread-name tiers, in STRICT precedence order:
#
#   1. "engine-<domain>-<n>"   the current domain factories. AUTHORITATIVE.
#   2. "engine-rpc-<n>"        the legacy engine-host factory. FALLBACK ONLY.
#   3. "pool-<n>-thread-<m>"   the JDK default. LAST FALLBACK ONLY.
#
# The fallback exists because `Executors.newFixedThreadPool(8)` with no factory names
# its threads from a PROCESS-GLOBAL counter in the JDK: any library that creates a
# pool first shifts the RPC pool to pool-2-*, and a predicate hardcoded to pool-1
# then counts zero for a genuine permanent deadlock and never recovers it. That was
# reproduced end to end, so the fallback must not be anchored to pool-1 either.
#
# The current domains are http, source, extension, deadline, and network. They are
# emitted and judged separately: four blocked HTTP threads plus eight running source
# threads is never reported as one twelve-thread pool. If any current domain appears,
# both fallback tiers are ignored. Otherwise engine-rpc wins over the unowned JDK
# fallback. No tier and no domain is ever merged with another.
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
#     <domain> <seen> <blocked> <monitor>
#
#   domain   http/source/extension/deadline/network, legacy, jdk, or none
#   seen     threads found for that one domain (see the precedence rule above)
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
        echo "none 0 0 unknown"
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
        function monitor_or_unknown(value) { return value == "" ? "unknown" : value }
        function named_total() { return http_seen + source_seen + extension_seen + deadline_seen + network_seen }
        function build(    output) {
            if (named_total() > 0) {
                output = "http " http_seen " " http_blocked " " monitor_or_unknown(http_monitor)
                output = output "\nsource " source_seen " " source_blocked " " monitor_or_unknown(source_monitor)
                output = output "\nextension " extension_seen " " extension_blocked " " monitor_or_unknown(extension_monitor)
                output = output "\ndeadline " deadline_seen " " deadline_blocked " " monitor_or_unknown(deadline_monitor)
                output = output "\nnetwork " network_seen " " network_blocked " " monitor_or_unknown(network_monitor)
            } else if (legacy_seen > 0) {
                output = "legacy " legacy_seen " " legacy_blocked " " monitor_or_unknown(legacy_monitor)
            } else if (pool_seen > 0) {
                output = "jdk " pool_seen " " pool_blocked " " monitor_or_unknown(pool_monitor)
            } else {
                output = "none 0 0 unknown"
            }
            return output
        }
        function reset() {
            http_seen = 0; http_blocked = 0; http_monitor = ""
            source_seen = 0; source_blocked = 0; source_monitor = ""
            extension_seen = 0; extension_blocked = 0; extension_monitor = ""
            deadline_seen = 0; deadline_blocked = 0; deadline_monitor = ""
            network_seen = 0; network_blocked = 0; network_monitor = ""
            legacy_seen = 0; legacy_blocked = 0; legacy_monitor = ""
            pool_seen = 0; pool_blocked = 0; pool_monitor = ""
        }
        /^Full thread dump/ {
            started = 1
            reset()
            kind = ""; pending = 0
            next
        }
        /^JNI global ref/ {
            if (started) {
                complete_output = build()
                have_complete = 1
            }
            next
        }
        /^"/ {
            pending = 0; kind = ""
            if ($0 ~ /^"engine-http-[0-9]+" /) { kind = "http"; http_seen++; pending = 1 }
            else if ($0 ~ /^"engine-source-[0-9]+" /) { kind = "source"; source_seen++; pending = 1 }
            else if ($0 ~ /^"engine-extension-[0-9]+" /) { kind = "extension"; extension_seen++; pending = 1 }
            else if ($0 ~ /^"engine-deadline-[0-9]+" /) { kind = "deadline"; deadline_seen++; pending = 1 }
            else if ($0 ~ /^"engine-network-[0-9]+" /) { kind = "network"; network_seen++; pending = 1 }
            else if ($0 ~ /^"engine-rpc-[0-9]+" /) { kind = "legacy"; legacy_seen++; pending = 1 }
            else if ($0 ~ /^"pool-[0-9]+-thread-[0-9]+" /) { kind = "pool"; pool_seen++; pending = 1 }
            next
        }
        pending == 1 {
            if ($0 ~ /java\.lang\.Thread\.State: BLOCKED \(on object monitor\)/) {
                if (kind == "http") { http_blocked++ }
                else if (kind == "source") { source_blocked++ }
                else if (kind == "extension") { extension_blocked++ }
                else if (kind == "deadline") { deadline_blocked++ }
                else if (kind == "network") { network_blocked++ }
                else if (kind == "legacy") { legacy_blocked++ }
                else { pool_blocked++ }
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
                if (kind == "http" && http_monitor == "") { http_monitor = addr " " cls }
                else if (kind == "source" && source_monitor == "") { source_monitor = addr " " cls }
                else if (kind == "extension" && extension_monitor == "") { extension_monitor = addr " " cls }
                else if (kind == "deadline" && deadline_monitor == "") { deadline_monitor = addr " " cls }
                else if (kind == "network" && network_monitor == "") { network_monitor = addr " " cls }
                else if (kind == "legacy" && legacy_monitor == "") { legacy_monitor = addr " " cls }
                else if (kind == "pool" && pool_monitor == "") { pool_monitor = addr " " cls }
            }
            next
        }
        END {
            if (have_complete) {
                print complete_output
            } else if (started) {
                print build()
            } else {
                print "none 0 0 unknown"
            }
        }
    ' "$1"
}

# wedge_held_monitor FILE — just the monitor field of wedge_scan, for reading a saved
# dump by hand and for the tests. The supervision loop does NOT call this: it parses
# all three fields out of its single wedge_scan pass, because a wedged JVM's dump can
# be megabytes and reading it twice buys nothing.
wedge_held_monitor() {
    monitor=$(wedge_scan "$1" | awk -v wanted="${2:-}" 'wanted == "" || $1 == wanted { print; exit }')
    monitor=${monitor#* }
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

# _status_parse FILE — validate and reduce the bounded /status contract. Output is
# tab-separated: sequence, oldest-ms, running, workers, running fingerprint, and a
# whitespace-free approved-fields-only JSON snapshot. Any missing, duplicate,
# malformed, oversized, or unapproved field fails closed.
_status_parse() {
    sp_file=$1
    if [ ! -r "$sp_file" ]; then
        return 1
    fi
    sp_size=$(wc -c 2>/dev/null < "$sp_file" || echo 0)
    case "${sp_size:-}" in
        ''|*[!0-9]*) return 1 ;;
    esac
    if [ "$sp_size" -gt 32768 ]; then
        return 1
    fi

    awk '
        function fail() { bad = 1 }
        function top_allowed(key) {
            return key == "ready" || key == "source_workers" || key == "per_source_limit" ||
                key == "queued" || key == "running" || key == "completion_sequence" ||
                key == "oldest_running_millis" || key == "completed" || key == "cancelled" ||
                key == "timed_out" || key == "rejected" || key == "extension_running" ||
                key == "extension_queued"
        }
        function scan_keys(text, source,    rest, token, key) {
            rest = text
            while (match(rest, /"[^"]+":/)) {
                token = substr(rest, RSTART, RLENGTH)
                key = substr(token, 2, length(token) - 3)
                if (source) {
                    if (key != "source_id" && key != "queued" && key != "running") fail()
                } else {
                    if (!top_allowed(key)) fail()
                    top_count[key]++
                }
                rest = substr(rest, RSTART + RLENGTH)
            }
        }
        function number(text, key,    re, token) {
            re = "\"" key "\":[0-9]+"
            if (!match(text, re)) { fail(); return 0 }
            token = substr(text, RSTART, RLENGTH)
            sub(/^.*:/, "", token)
            return token
        }
        function boolean(text, key,    re, token) {
            re = "\"" key "\":(true|false)"
            if (!match(text, re)) { fail(); return "false" }
            token = substr(text, RSTART, RLENGTH)
            sub(/^.*:/, "", token)
            return token
        }
        function object_number(text, key,    re, token, copy) {
            re = "\"" key "\":[0-9]+"
            copy = text
            if (!match(copy, re)) { fail(); return 0 }
            token = substr(copy, RSTART, RLENGTH)
            copy = substr(copy, RSTART + RLENGTH)
            if (match(copy, re)) fail()
            sub(/^.*:/, "", token)
            return token
        }
        function decimal_less(left, right) {
            if (length(left) != length(right)) return length(left) < length(right)
            return ("x" left) < ("x" right)
        }
        {
            input = input $0
        }
        END {
            gsub(/[[:space:]]/, "", input)
            if (input !~ /^\{.*\}$/ || input ~ /:"/) fail()

            marker = "\"busiest_sources\":["
            start = index(input, marker)
            if (start == 0) fail()
            after = substr(input, start + length(marker))
            array_end = index(after, "]")
            if (array_end == 0) fail()
            array = substr(after, 1, array_end - 1)
            prefix = substr(input, 1, start - 1)
            suffix = substr(after, array_end + 1)
            if (index(suffix, marker) != 0) fail()

            scan_keys(prefix suffix, 0)
            required = "ready source_workers per_source_limit queued running completion_sequence oldest_running_millis completed cancelled timed_out rejected extension_running extension_queued"
            split(required, required_keys, " ")
            for (i in required_keys) if (top_count[required_keys[i]] != 1) fail()

            ready = boolean(prefix suffix, "ready")
            workers = number(prefix suffix, "source_workers")
            per_source = number(prefix suffix, "per_source_limit")
            queued = number(prefix suffix, "queued")
            running = number(prefix suffix, "running")
            sequence = number(prefix suffix, "completion_sequence")
            oldest = number(prefix suffix, "oldest_running_millis")
            completed = number(prefix suffix, "completed")
            cancelled = number(prefix suffix, "cancelled")
            timed_out = number(prefix suffix, "timed_out")
            rejected = number(prefix suffix, "rejected")
            extension_running = boolean(prefix suffix, "extension_running")
            extension_queued = number(prefix suffix, "extension_queued")

            fingerprint = workers "|" running "|"
            source_json = ""
            source_count = 0
            running_sum = 0
            if (array != "") {
                rest = array
                gsub(/\},\{/, "}\n{", rest)
                count = split(rest, objects, "\n")
                if (count > 10) fail()
                for (i = 1; i <= count; i++) {
                    object = objects[i]
                    if (object !~ /^\{.*\}$/) fail()
                    scan_keys(object, 1)
                    source_id = object_number(object, "source_id")
                    source_queued = object_number(object, "queued")
                    source_running = object_number(object, "running")
                    source_key = "x" source_id
                    if (source_key in seen_source_ids) fail()
                    seen_source_ids[source_key] = 1
                    if (source_json != "") source_json = source_json ","
                    source_json = source_json "{\"source_id\":" source_id ",\"queued\":" source_queued ",\"running\":" source_running "}"
                    if (source_running > 0) {
                        source_count++
                        running_source_ids[source_count] = source_id
                        running_source_counts[source_count] = source_running
                        running_sum += source_running
                    }
                }
            }
            if (running_sum != running) fail()

            for (i = 1; i < source_count; i++) {
                for (j = i + 1; j <= source_count; j++) {
                    if (decimal_less(running_source_ids[j], running_source_ids[i])) {
                        swap = running_source_ids[i]
                        running_source_ids[i] = running_source_ids[j]
                        running_source_ids[j] = swap
                        swap = running_source_counts[i]
                        running_source_counts[i] = running_source_counts[j]
                        running_source_counts[j] = swap
                    }
                }
            }
            for (i = 1; i <= source_count; i++) {
                if (i > 1) fingerprint = fingerprint ","
                fingerprint = fingerprint running_source_ids[i] ":" running_source_counts[i]
            }

            approved = "{\"ready\":" ready ",\"source_workers\":" workers ",\"per_source_limit\":" per_source ",\"queued\":" queued ",\"running\":" running ",\"completion_sequence\":" sequence ",\"oldest_running_millis\":" oldest ",\"completed\":" completed ",\"cancelled\":" cancelled ",\"timed_out\":" timed_out ",\"rejected\":" rejected ",\"busiest_sources\":[" source_json "],\"extension_running\":" extension_running ",\"extension_queued\":" extension_queued "}"
            if (input != approved) fail()
            if (bad) exit 1
            printf "%s\t%s\t%s\t%s\t%s\t%s\n", sequence, oldest, running, workers, fingerprint, approved
        }
    ' "$sp_file"
}

# status_fingerprint FILE — the exact approved physical-running-work identity.
# Completion sequence and age are evaluated separately, and queue depth is never a
# recovery predicate.
status_fingerprint() {
    sf_parsed=$(_status_parse "$1") || return 1
    printf '%s\n' "$sf_parsed" | awk -F '\t' '{ print $5 }'
}

# _source_dump_parse FILE — require one complete HotSpot dump whose entire
# engine-source population is either in a timed wait or visibly inside network I/O.
# Prints a stable population fingerprint and a redacted evidence excerpt separated
# by one tab. Raw stacks never enter diagnostics.
_source_dump_parse() {
    if [ ! -r "$1" ]; then
        return 1
    fi
    awk '
        function reset(    i) {
            delete names; delete states; delete network
            count = 0; current = ""; started = 1; invalid = 0; max_id = 0
            complete = ""; dump_complete = 0
        }
        function finish_thread(    category) {
            if (current == "") return
            if (states[current] ~ /^TIMED_WAITING/) category = "timed-wait"
            else if (network[current] && states[current] ~ /^(RUNNABLE|WAITING)/) category = "network-wait"
            else invalid = 1
                split(states[current], state_parts, " ")
                states[current] = state_parts[1]
                evidence[current] = category
            current = ""
        }
        function build(    i, signature, excerpt, name) {
            finish_thread()
            if (invalid || count == 0) return ""
            for (i = 1; i <= max_id; i++) {
                name = "engine-source-" i
                if (!(name in names)) return ""
                if (signature != "") { signature = signature ","; excerpt = excerpt ";" }
                signature = signature name
                excerpt = excerpt name " state=" states[name] " evidence=" evidence[name]
            }
            if (count != max_id) return ""
            return signature "\t" excerpt
        }
        /^Full thread dump/ { reset(); next }
        started && /^"engine-source-[0-9]+" / {
            finish_thread()
            name = $0
            sub(/^"/, "", name); sub(/" .*/, "", name)
            id = name; sub(/^engine-source-/, "", id)
            if (name in names || id !~ /^[0-9]+$/ || id < 1) invalid = 1
            names[name] = 1; count++; if (id > max_id) max_id = id
            current = name
            next
        }
        started && /^"/ {
            finish_thread()
            next
        }
        started && current != "" && /java\.lang\.Thread\.State:/ {
            state = $0
            sub(/^.*State:[[:space:]]*/, "", state)
            states[current] = state
            next
        }
        started && current != "" && /(sun\.nio\.ch\.|java\.net\.Socket|okhttp3\.|okio\.|SocketDispatcher|Net\.poll|EPoll\.wait)/ {
            network[current] = 1
            next
        }
        started && /^JNI global ref/ {
            complete = build()
            if (complete != "") dump_complete = 1
            next
        }
        END {
            if (!dump_complete) exit 1
            print complete
        }
    ' "$1"
}

# exhaustion_sample STATUS_FILE DUMP_FILE — one pure, bounded evidence record.
exhaustion_sample() {
    es_status=$(_status_parse "$1") || return 1
    es_dump=$(_source_dump_parse "$2") || return 1
    es_tab=$(printf '\t')
    IFS="$es_tab" read -r es_sequence es_oldest es_running es_workers es_fingerprint _ <<EOF
$es_status
EOF
    es_dump_fingerprint=$(printf '%s\n' "$es_dump" | awk -F '\t' '{ print $1 }')
    printf '%s %s %s %s %s %s\n' "$es_sequence" "$es_oldest" "$es_running" "$es_workers" "$es_fingerprint" "$es_dump_fingerprint"
}

# exhaustion_verdict consecutive oldest_ms running workers progress_same dump_same
# — the exact conservative restart gate. Malformed input always declines.
exhaustion_verdict() {
    for ev_value in "$@"; do
        case "$ev_value" in
            ''|*[!0-9]*) echo "decline"; return 0 ;;
        esac
    done
    if [ "$#" -eq 6 ] && [ "$1" -eq 6 ] && [ "$2" -gt 180000 ] &&
       [ "$3" -eq 8 ] && [ "$4" -eq 8 ] && [ "$5" -eq 1 ] && [ "$6" -eq 1 ]; then
        echo "restart"
    else
        echo "decline"
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
WATCHDOG_STATUS_URL=http://127.0.0.1:${TSUNDOKU_ENGINE_PORT:-7777}/status
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

# Internal evidence paths. These are process-local files, not owner-facing safety
# settings; the thresholds and caps remain fixed by the recovery contract.
WATCHDOG_STATUS_FILE=/tmp/engine-host.status
WATCHDOG_EXHAUSTION_FIRST_DUMP=/tmp/engine-host.exhaustion-first.dump
WATCHDOG_EXHAUSTION_SIXTH_DUMP=/tmp/engine-host.exhaustion-sixth.dump
WATCHDOG_DIAGNOSTIC_FILE=/tmp/engine-host.watchdog-diagnostic

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

watchdog_probe_health() {
    curl -fsS --max-time "$WATCHDOG_PROBE_TIMEOUT" "$WATCHDOG_HEALTH_URL" >/dev/null 2>&1
}

# watchdog_fetch_status FILE — fetch one bounded status snapshot. curl's size bound
# stops an oversized body during transfer; the parser repeats the check before use.
watchdog_fetch_status() {
    if ! curl -fsS --max-time "$WATCHDOG_PROBE_TIMEOUT" --max-filesize 32768 \
        "$WATCHDOG_STATUS_URL" > "$1" 2>/dev/null; then
        : > "$1"
        return 1
    fi
    return 0
}

watchdog_engine_pid() {
    cat "$WATCHDOG_PID_FILE" 2>/dev/null || echo ""
}

# watchdog_capture_dump PID FILE — capture only output appended by this SIGQUIT.
watchdog_capture_dump() {
    cd_pid=$1
    cd_file=$2
    cd_offset=$(watchdog_log_size)
    cd_offset=${cd_offset:-0}
    kill -3 "$cd_pid" 2>/dev/null || return 1
    sleep "$WATCHDOG_DUMP_SETTLE"
    tail -c "+$((cd_offset + 1))" "$WATCHDOG_LOG_FILE" > "$cd_file" 2>/dev/null || return 1
    return 0
}

# watchdog_write_exhaustion_diagnostics STATUS FIRST_DUMP SIXTH_DUMP PID FILE
# Reconstruct a bounded bundle from approved fields only. Neither raw status nor raw
# stack text is copied, so unexpected payloads, local values, and secrets cannot leak.
watchdog_write_exhaustion_diagnostics() {
    wd_status=$(_status_parse "$1") || return 1
    wd_first=$(_source_dump_parse "$2") || return 1
    wd_sixth=$(_source_dump_parse "$3") || return 1
    case "$4" in
        ''|*[!0-9]*) return 1 ;;
    esac
    wd_tab=$(printf '\t')
    IFS="$wd_tab" read -r _ _ _ _ _ wd_approved <<EOF
$wd_status
EOF
    wd_first_excerpt=$(printf '%s\n' "$wd_first" | awk -F '\t' '{ print $2 }')
    wd_sixth_excerpt=$(printf '%s\n' "$wd_sixth" | awk -F '\t' '{ print $2 }')
    wd_status_size=$(printf '%s' "$wd_approved" | wc -c)
    wd_thread_size=$(printf '%s\n%s' "$wd_first_excerpt" "$wd_sixth_excerpt" | wc -c)
    if [ "$wd_status_size" -gt 32768 ] || [ "$wd_thread_size" -gt 262144 ]; then
        return 1
    fi
    wd_now=$(date +%s 2>/dev/null || echo 0)
    wd_now=${wd_now:-0}
    wd_tmp=$5.tmp.$$
    umask 077
    {
        printf 'decision=sustained-source-exhaustion\n'
        printf 'timestamp_epoch=%s\n' "$wd_now"
        printf 'engine_pid=%s\n' "$4"
        printf 'profile=default\n'
        printf 'status=%s\n' "$wd_approved"
        printf 'first_dump=%s\n' "$wd_first_excerpt"
        printf 'sixth_dump=%s\n' "$wd_sixth_excerpt"
    } > "$wd_tmp" || return 1
    mv "$wd_tmp" "$5" || return 1
    return 0
}

watchdog_reset_exhaustion() {
    exhaustion_consecutive=0
    exhaustion_sequence=""
    exhaustion_fingerprint=""
    exhaustion_first_dump_fingerprint=""
    return 0
}

# watchdog_start_exhaustion STATUS_PARSE — begin an episode and capture its only
# first-sample dump. Unknown or truncated evidence declines the episode immediately.
watchdog_start_exhaustion() {
    se_status=$1
    se_tab=$(printf '\t')
    IFS="$se_tab" read -r se_sequence _ _ _ se_fingerprint _ <<EOF
$se_status
EOF
    se_pid=$(watchdog_engine_pid)
    case "$se_pid" in
        ''|*[!0-9]*)
            watchdog_log "WARNING: source exhaustion status had no live engine pid; evidence reset"
            watchdog_reset_exhaustion
            return 0
            ;;
    esac
    if ! watchdog_capture_dump "$se_pid" "$WATCHDOG_EXHAUSTION_FIRST_DUMP"; then
        watchdog_log "WARNING: first source-exhaustion thread dump was unreadable; evidence reset, not restarting"
        watchdog_reset_exhaustion
        return 0
    fi
    se_dump=$(_source_dump_parse "$WATCHDOG_EXHAUSTION_FIRST_DUMP") || se_dump=""
    if [ -z "$se_dump" ]; then
        watchdog_log "WARNING: first source-exhaustion thread dump was unknown or truncated; evidence reset, not restarting"
        watchdog_reset_exhaustion
        return 0
    fi
    se_dump_fingerprint=$(printf '%s\n' "$se_dump" | awk -F '\t' '{ print $1 }')
    se_dump_count=$(printf '%s' "$se_dump_fingerprint" | awk -F ',' '{ print NF }')
    if [ "$se_dump_count" -ne 8 ]; then
        watchdog_log "WARNING: first source-exhaustion dump contained ${se_dump_count} source workers, not 8; evidence reset, not restarting"
        watchdog_reset_exhaustion
        return 0
    fi
    exhaustion_consecutive=1
    exhaustion_sequence=$se_sequence
    exhaustion_fingerprint=$se_fingerprint
    exhaustion_first_dump_fingerprint=$se_dump_fingerprint
    watchdog_log "source exhaustion episode started: 1 of 6 stable samples"
    return 0
}

# watchdog_evaluate_exhaustion — evaluate the status already fetched into the fixed
# status file. This path runs only after a successful /health probe.
watchdog_evaluate_exhaustion() {
    ee_status=$(_status_parse "$WATCHDOG_STATUS_FILE") || ee_status=""
    if [ -z "$ee_status" ]; then
        watchdog_log "WARNING: /status evidence was malformed, unreadable, oversized, or contained unapproved fields; evidence reset, not restarting"
        watchdog_reset_exhaustion
        return 0
    fi
    ee_tab=$(printf '\t')
    IFS="$ee_tab" read -r ee_sequence ee_oldest ee_running ee_workers ee_fingerprint _ <<EOF
$ee_status
EOF
    if [ "$ee_running" -ne 8 ] || [ "$ee_workers" -ne 8 ] || [ "$ee_oldest" -le 180000 ]; then
        watchdog_reset_exhaustion
        return 0
    fi

    if [ "$exhaustion_consecutive" -eq 0 ]; then
        watchdog_start_exhaustion "$ee_status"
        return 0
    fi
    if [ "$ee_sequence" != "$exhaustion_sequence" ] || [ "$ee_fingerprint" != "$exhaustion_fingerprint" ]; then
        watchdog_log "source exhaustion progress or running fingerprint changed; evidence reset"
        watchdog_reset_exhaustion
        watchdog_start_exhaustion "$ee_status"
        return 0
    fi

    exhaustion_consecutive=$((exhaustion_consecutive + 1))
    if [ "$exhaustion_consecutive" -lt 6 ]; then
        watchdog_log "source exhaustion evidence: ${exhaustion_consecutive} of 6 stable samples"
        return 0
    fi

    ee_pid=$(watchdog_engine_pid)
    case "$ee_pid" in
        ''|*[!0-9]*)
            watchdog_log "WARNING: sixth source-exhaustion sample had no live engine pid; evidence reset"
            watchdog_reset_exhaustion
            return 0
            ;;
    esac
    if ! watchdog_capture_dump "$ee_pid" "$WATCHDOG_EXHAUSTION_SIXTH_DUMP"; then
        watchdog_log "WARNING: sixth source-exhaustion thread dump was unreadable; evidence reset, not restarting"
        watchdog_reset_exhaustion
        return 0
    fi
    ee_dump=$(_source_dump_parse "$WATCHDOG_EXHAUSTION_SIXTH_DUMP") || ee_dump=""
    if [ -z "$ee_dump" ]; then
        watchdog_log "WARNING: sixth source-exhaustion thread dump was unknown or truncated; evidence reset, not restarting"
        watchdog_reset_exhaustion
        return 0
    fi
    ee_dump_fingerprint=$(printf '%s\n' "$ee_dump" | awk -F '\t' '{ print $1 }')
    ee_dump_same=0
    if [ "$ee_dump_fingerprint" = "$exhaustion_first_dump_fingerprint" ]; then
        ee_dump_same=1
    fi

    # The dump settle window is part of the proof, not a blind spot. A physical call
    # can complete while SIGQUIT is being written and be replaced by queued work on
    # the same named workers, so re-fetch bounded status after the dump and compare it
    # with the episode baseline before diagnostics or process control.
    if ! watchdog_fetch_status "$WATCHDOG_STATUS_FILE"; then
        watchdog_log "WARNING: post-dump /status evidence was unreadable or oversized; evidence reset, not restarting"
        watchdog_reset_exhaustion
        return 0
    fi
    ee_final_status=$(_status_parse "$WATCHDOG_STATUS_FILE") || ee_final_status=""
    if [ -z "$ee_final_status" ]; then
        watchdog_log "WARNING: post-dump /status evidence was malformed or contained unapproved fields; evidence reset, not restarting"
        watchdog_reset_exhaustion
        return 0
    fi
    IFS="$ee_tab" read -r ee_final_sequence ee_final_oldest ee_final_running ee_final_workers ee_final_fingerprint _ <<EOF
$ee_final_status
EOF
    ee_final_pid=$(watchdog_engine_pid)
    case "$ee_final_pid" in
        ''|*[!0-9]*) ee_final_pid="" ;;
    esac
    ee_progress_same=0
    if [ "$ee_final_sequence" = "$exhaustion_sequence" ] &&
       [ "$ee_final_fingerprint" = "$exhaustion_fingerprint" ] &&
       [ "$ee_final_pid" = "$ee_pid" ]; then
        ee_progress_same=1
    fi
    ee_verdict=$(exhaustion_verdict "$exhaustion_consecutive" "$ee_final_oldest" "$ee_final_running" "$ee_final_workers" "$ee_progress_same" "$ee_dump_same")
    if [ "$ee_verdict" != "restart" ]; then
        watchdog_log "post-dump status, engine identity, or source-worker population changed; evidence reset, not restarting"
        watchdog_reset_exhaustion
        return 0
    fi

    if ! watchdog_write_exhaustion_diagnostics "$WATCHDOG_STATUS_FILE" \
        "$WATCHDOG_EXHAUSTION_FIRST_DUMP" "$WATCHDOG_EXHAUSTION_SIXTH_DUMP" \
        "$ee_pid" "$WATCHDOG_DIAGNOSTIC_FILE"; then
        watchdog_log "WARNING: bounded source-exhaustion diagnostics could not be written; not restarting"
        watchdog_reset_exhaustion
        return 0
    fi
    watchdog_log "SOURCE EXHAUSTION CONFIRMED: 8 of 8 source workers unchanged for 6 samples; diagnostics written to ${WATCHDOG_DIAGNOSTIC_FILE}"

    ee_now=$(date +%s 2>/dev/null || echo 0)
    ee_now=${ee_now:-0}
    if [ $((ee_now - watchdog_last_kill)) -lt "$WATCHDOG_COOLDOWN" ]; then
        watchdog_log "last restart was $((ee_now - watchdog_last_kill))s ago; holding off (thrash guard)"
        watchdog_reset_exhaustion
        return 0
    fi
    ee_stop_pid=$(watchdog_engine_pid)
    if [ "$ee_stop_pid" != "$ee_pid" ]; then
        watchdog_log "engine pid changed after source-exhaustion diagnostics; evidence reset, not restarting"
        watchdog_reset_exhaustion
        return 0
    fi
    watchdog_last_kill=$ee_now
    watchdog_reset_exhaustion
    watchdog_stop_engine "$ee_pid"
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
    armed=0
    fails=0
    # Cooldown state lives in globals initialised only when unset, so a loop that
    # dies and is re-entered by supervise_engine_health does not forget when it last
    # dumped or killed — a forgetful restart would walk straight past the thrash
    # guard.
    watchdog_last_dump=${watchdog_last_dump:-0}
    watchdog_last_kill=${watchdog_last_kill:-0}
    watchdog_reset_exhaustion

    while true; do
        sleep "$WATCHDOG_PROBE_INTERVAL"
        watchdog_trim_log

        if watchdog_probe_health; then
            if [ "$armed" -eq 0 ]; then
                armed=1
                watchdog_log "wedge watchdog armed: health supervision armed after first successful response"
            fi
            fails=0
            if watchdog_fetch_status "$WATCHDOG_STATUS_FILE"; then
                watchdog_evaluate_exhaustion
            else
                watchdog_log "WARNING: /status evidence was unreadable or oversized; evidence reset, not restarting"
                watchdog_reset_exhaustion
            fi
            continue
        fi

        # Sustained exhaustion requires responsive control capacity. A failed health
        # probe therefore resets that evidence before the independent all-BLOCKED path.
        watchdog_reset_exhaustion

        # Startup grace is state, not a one-time launch gate. Until this process has
        # answered at least one bounded health probe, silence is boot evidence only.
        if [ "$armed" -eq 0 ]; then
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
        scan=$(wedge_scan "$WATCHDOG_DUMP_FILE" 2>/dev/null || echo "none 0 0 unknown")
        scan=${scan:-none 0 0 unknown}
        wedge_domain=""
        wedge_seen=0
        wedge_monitor=unknown
        while read -r domain seen blocked monitor; do
            seen=${seen:-0}
            blocked=${blocked:-0}
            case "$seen:$blocked" in
                *[!0-9:]*|:*)
                    watchdog_log "WARNING: malformed ${domain:-unknown} pool evidence; not restarting (GAP-137)"
                    continue
                    ;;
            esac
            if [ "$seen" -eq 0 ]; then
                continue
            fi
            verdict=$(wedge_verdict "$seen" "$blocked" || echo "insufficient")
            if [ "$verdict" = "wedge" ] && [ -z "$wedge_domain" ]; then
                wedge_domain=$domain
                wedge_seen=$seen
                wedge_monitor=${monitor:-unknown}
            elif [ "$verdict" = "insufficient" ]; then
                watchdog_log "WARNING: only ${seen} ${domain} pool thread(s) in the dump region (need ${WATCHDOG_POOL_MIN_SEEN} to judge); the dump looks truncated. Not restarting (GAP-137)"
            elif [ "$verdict" = "saturated" ]; then
                watchdog_log "${blocked} of ${seen} ${domain} pool threads BLOCKED; the rest are running — engine is SATURATED, not deadlocked — not restarting"
            fi
        done <<EOF
$scan
EOF

        if [ -z "$wedge_domain" ]; then
            case "$scan" in
                none\ *)
                    watchdog_log "WARNING: no engine execution-pool threads found in the dump region — the watchdog cannot tell wedged from busy. The thread names may have changed, the dump may not have landed within ${WATCHDOG_DUMP_SETTLE}s, or the JVM may be running with -Xrs (which ignores SIGQUIT). Not restarting (GAP-137)"
                    ;;
            esac
            continue
        fi

        if [ "$wedge_domain" = "legacy" ]; then
            watchdog_log "WEDGE CONFIRMED: all ${wedge_seen} RPC pool threads BLOCKED on ${wedge_monitor} (GAP-137)"
        else
            watchdog_log "WEDGE CONFIRMED: all ${wedge_seen} ${wedge_domain} pool threads BLOCKED on ${wedge_monitor} (GAP-137)"
        fi

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
# Starts with the engine supervisor and arms only after the first successful bounded
# health response, so a late-ready engine gains supervision without treating boot as
# failure evidence.
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
