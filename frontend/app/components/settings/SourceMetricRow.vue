<script setup lang="ts">
import { computed } from 'vue'
import { useNow } from '../../composables/useNow'
import { formatRetryEta } from '../../utils/retryEta'
import AppButton from '../ui/AppButton.vue'
import type { SourceMetric, SourceWarmth } from '../screens/settings.types'

/**
 * SourceMetricRow — one source's search-performance line in the Source Metrics
 * pane: the source name, a warm/cold session badge, an amber "Slow" badge when
 * the backend flags it slow, a danger "Erroring" badge when a last error is
 * present, then the last/avg latency, the success rate, and (when erroring) the
 * truncated last-error text (full message on hover via `title`).
 *
 * When the source's anti-ban circuit-breaker is tripped (`source.breaker.
 * isCoolingDown`) the row also shows a "⏸ cooling down · retry ~Nm (N failures)"
 * banner (the breaker's last error rides in the tooltip) plus a "Reset" button
 * that force-clears the cooldown — the owner escape from a wedged source. The
 * retry ETA is computed CLIENT-SIDE from `cooldownUntil` against the shared
 * ticking clock (useNow), so it counts down live without a refetch.
 *
 * Emits `reset` (the source id) when the owner clicks Reset; the row is otherwise
 * presentation-only. Token-only colours so it reads in both themes.
 *
 *   - `source`: the source metric to render.
 *   - `resetting`: this row's reset is in flight (spins its Reset button).
 */
const props = defineProps<{
  /** The source metric snapshot to render. */
  source: SourceMetric
  /** Whether this source's breaker reset is in flight. */
  resetting?: boolean
}>()

const emit = defineEmits<{
  /** Reset this source's tripped circuit-breaker — carries the source id. */
  reset: [id: string]
}>()

const { now } = useNow()

// The breaker is cooling down (tripped) — drives the cooldown banner + Reset button.
const isCoolingDown = computed(() => props.source.breaker?.isCoolingDown === true)

// Live "~Nm" / "~Ns" / "~Nh" until the cooldown elapses (recomputes each tick).
const retryEta = computed(() => {
  const until = props.source.breaker?.cooldownUntil
  return until ? formatRetryEta(until, now.value) : ''
})

// The breaker's last error (why it tripped) — surfaced in the banner tooltip.
const breakerError = computed(() => props.source.breaker?.lastError ?? '')

// A source counts as "warm" only if it was warmed within this window — the
// anti-bot session is still fresh. Chosen to match Suwayomi's default
// FlareSolverr session TTL (~15 min): warmed longer ago than that and the
// session has most likely lapsed, so we call it "cold".
const WARM_RECENCY_MS = 15 * 60_000

/** Format a millisecond latency: "—" when unmeasured, "1.2s" ≥ 1s, else "320ms". */
function fmtLatency(ms: number): string {
  if (ms <= 0) return '—'
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${Math.round(ms)}ms`
}

/** Relative-time label for a timestamp (mirrors UnhealthySourceRow's `rel`). */
function rel(iso: string | null): string {
  if (iso == null) return 'never'
  const d = Date.now() - Date.parse(iso)
  const m = 60_000, h = 3_600_000, day = 86_400_000
  if (d < m) return 'just now'
  if (d < h) return `${Math.floor(d / m)}m ago`
  if (d < day) return `${Math.floor(d / h)}h ago`
  return `${Math.floor(d / day)}d ago`
}

// Warm/cold session state derived from how recently the source was warmed.
const warmth = computed<SourceWarmth>(() => {
  const w = props.source.lastWarmedAt
  if (w == null) return 'never'
  return Date.now() - Date.parse(w) <= WARM_RECENCY_MS ? 'warm' : 'cold'
})
const WARMTH_LABELS: Record<SourceWarmth, string> = {
  warm: 'Warm',
  cold: 'Cold',
  never: 'Never warmed',
}
const warmthLabel = computed(() => WARMTH_LABELS[warmth.value])
// Tooltip: when the source was last warmed (warm/cold rows only).
const warmthTitle = computed(() =>
  props.source.lastWarmedAt == null ? 'Never warmed' : `Warmed ${rel(props.source.lastWarmedAt)}`)

const hasError = computed(() => props.source.lastError !== '')

// Success rate (percentage) — null when the source has no recorded searches yet.
const successRate = computed<number | null>(() => {
  const { searchCount, successCount } = props.source
  return searchCount > 0 ? Math.round((successCount / searchCount) * 100) : null
})
const successLabel = computed(() =>
  successRate.value == null
    ? 'no searches yet'
    : `${successRate.value}% success · ${props.source.successCount}/${props.source.searchCount}`)
</script>

<template>
  <div class="metric" :class="{ 'metric--slow': source.isSlow, 'metric--erroring': hasError, 'metric--cooling': isCoolingDown }">
    <div class="metric__head">
      <span class="metric__name">{{ source.name }}</span>
      <span class="metric__warmth" :class="`metric__warmth--${warmth}`" :title="warmthTitle">
        <span class="metric__dot" aria-hidden="true" />
        {{ warmthLabel }}
      </span>
      <span v-if="isCoolingDown" class="metric__badge metric__badge--cooling">Cooling down</span>
      <span v-if="source.isSlow" class="metric__badge metric__badge--slow">Slow</span>
      <span v-if="hasError" class="metric__badge metric__badge--error" :title="source.lastError">Erroring</span>
    </div>

    <div class="metric__stats">
      <span class="metric__stat">last <b>{{ fmtLatency(source.lastLatencyMs) }}</b></span>
      <span class="metric__stat">avg <b>{{ fmtLatency(source.avgLatencyMs) }}</b></span>
      <span class="metric__stat">{{ successLabel }}</span>
      <span v-if="source.lastWarmedAt" class="metric__stat metric__stat--faint">warmed {{ rel(source.lastWarmedAt) }}</span>
    </div>

    <!-- Anti-ban breaker banner: the engine is refusing this source until the
         cooldown elapses. The owner can force-clear it with Reset. -->
    <div v-if="isCoolingDown" class="metric__cooldown">
      <span class="metric__cooldown-text" :title="breakerError || undefined">
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="6" y="4" width="4" height="16" rx="1" /><rect x="14" y="4" width="4" height="16" rx="1" /></svg>
        cooling down · retry {{ retryEta }}
        <span class="metric__cooldown-fails">({{ source.breaker?.consecutiveFailures }} failures)</span>
      </span>
      <AppButton variant="mini" size="xs" :loading="resetting" @click="emit('reset', source.id)">
        Reset
      </AppButton>
    </div>

    <div v-if="hasError" class="metric__error" :title="source.lastError">{{ source.lastError }}</div>
  </div>
</template>

<style scoped>
.metric {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 11px 13px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--surface2);
}

/* A slow source gets an amber left rule; an erroring one a danger rule (error
   wins visually — it's the more urgent signal). */
.metric--slow {
  border-left: 3px solid var(--set-update-text);
}

.metric--erroring {
  border-left: 3px solid var(--danger);
}

/* A cooling-down source gets a danger rule too — it is being actively refused,
   the most urgent signal (wins over slow). */
.metric--cooling {
  border-left: 3px solid var(--danger);
}

.metric__head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.metric__name {
  font-weight: var(--weight-bold);
  font-size: 13.5px;
  color: var(--text);
}

/* ---- Warm/cold session badge ---------------------------------------------- */
.metric__warmth {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
}

.metric__dot {
  width: 6px;
  height: 6px;
  border-radius: var(--radius-pill);
  flex-shrink: 0;
  background: currentcolor;
}

.metric__warmth--warm {
  color: var(--set-ok-text);
  background: var(--set-ok-bg);
}

/* Cold + never share the neutral treatment (a lapsed / absent session). */
.metric__warmth--cold,
.metric__warmth--never {
  color: var(--muted);
  background: var(--surface3);
}

/* ---- Slow + erroring badges ----------------------------------------------- */
.metric__badge {
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.03em;
  text-transform: uppercase;
  padding: 2px 7px;
  border-radius: var(--radius-pill);
}

.metric__badge--slow {
  color: var(--set-update-text);
  background: var(--set-update-bg);
}

.metric__badge--error {
  color: var(--danger-text);
  background: var(--danger-bg);
}

.metric__badge--cooling {
  color: var(--danger-text);
  background: var(--danger-bg);
}

/* ---- Cooldown banner ------------------------------------------------------- */
.metric__cooldown {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 7px 9px;
  border-radius: var(--radius-md);
  background: var(--danger-bg);
}

.metric__cooldown-text {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--danger-text);
}

/* The failure count is secondary detail — quieter than the headline. */
.metric__cooldown-fails {
  font-weight: var(--weight-semibold);
  opacity: 0.8;
}

/* ---- Stats line ------------------------------------------------------------ */
.metric__stats {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
  font-size: var(--text-xs);
  color: var(--muted);
}

.metric__stat b {
  font-family: var(--font-mono);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.metric__stat--faint {
  color: var(--faint);
}

/* ---- Last-error line ------------------------------------------------------- */
.metric__error {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--danger-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
