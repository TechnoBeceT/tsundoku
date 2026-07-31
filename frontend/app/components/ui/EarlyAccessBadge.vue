<script setup lang="ts">
import { computed } from 'vue'
import { useNow } from '../../composables/useNow'
import { formatRetryEta } from '../../utils/retryEta'

/**
 * EarlyAccessBadge — "this chapter is not published free yet".
 *
 * Some sources put their newest chapters behind coins for a few days and then
 * release them. The engine parks such a chapter instead of charging it (GAP-141),
 * so the chapter arrives on its own and NO owner action helps. Its `Chapter.state`
 * is nevertheless `failed` — the fetch produced no file — which is why callers
 * render this badge INSTEAD of the `StatusBadge`: a red "Failed" pill on a chapter
 * that is merely early-access sends the owner hunting for a fault that is not there.
 *
 * It is deliberately calm — the same muted pill language as `DeferralNote`, tinted
 * toward the accent rather than the danger tokens — and it names the return date
 * ("free ~3d") so the row reads as WAITING, not stuck. The ETA is computed
 * CLIENT-SIDE from the raw `until` timestamp against the shared ticking clock, so
 * it counts down without a refetch, exactly as the deferral pill does.
 *
 * Presentation only: the parent decides WHEN to render it (the row's `locked` flag)
 * and passes the backend's raw values straight through.
 */
const props = defineProps<{
  /**
   * When the withholding is expected to lapse (ISO 8601) — the backend's
   * `lockedUntil`. Omitted → the pill renders without a countdown.
   */
  until?: string
  /** The source's own message ("Chapter locked, coins required"), as a tooltip. */
  reason?: string
}>()

const { now } = useNow()

// Live "~3d" / "~5h" until the source releases the chapter; absent without an expiry.
const eta = computed(() => (props.until ? formatRetryEta(props.until, now.value) : ''))
</script>

<template>
  <span class="early" :title="reason">
    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="4" y="10" width="16" height="11" rx="2" />
      <path d="M8 10V7a4 4 0 0 1 8 0v3" />
    </svg>
    Early access<template v-if="eta"> · free {{ eta }}</template>
  </span>
</template>

<style scoped>
/* A calm, non-alarming pill: the chapter is not broken, only not free yet. The
   accent tint separates it at a glance from the danger-toned failure chrome it
   REPLACES, without shouting like a warning. Token-only so both themes read. */
.early {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: var(--space-2xs);
  font-size: 0.65625rem; /* 10.5px @16 — matches the sibling deferral/upgrade pills */
  font-weight: var(--weight-bold);
  padding: var(--space-3xs) var(--space-xs);
  border-radius: var(--radius-pill);
  background: var(--accentSoft);
  color: var(--accentBright);
  white-space: nowrap;
}
</style>
