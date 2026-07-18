<script setup lang="ts">
import { computed } from 'vue'
import { useNow } from '../../composables/useNow'
import { formatRetryEta } from '../../utils/retryEta'

/**
 * DeferralNote — the per-row "why this queued chapter is not moving" indicator.
 *
 * The download engine has tried this chapter's source and DEFERRED it: that source
 * is inside a persisted per-source cooldown (a failed download's backoff, or a
 * failed upgrade fetch's cooldown). Instead of the bare "Upgrade ready" / "Wanted"
 * — which reads as "any moment now" — this pill states the truth:
 * "⏳ waiting on <source> · retry ~Nm".
 *
 * The retry ETA is computed CLIENT-SIDE from `deferredUntil` against the shared
 * ticking clock (useNow), so it counts down live without a refetch. `reason` (the
 * source's last error) rides in the title tooltip — present, never shouted.
 *
 * Presentation only: the parent decides WHEN to render it (row.deferredUntil set)
 * and supplies the waited-on source NAME (already on the row: the upgrade target for
 * an upgrade, the primary source for a wanted chapter — never re-derived here).
 */
const props = defineProps<{
  /** The source's next_attempt_at (ISO 8601) — a FUTURE instant; drives the ETA. */
  deferredUntil: string
  /** Display name of the source being waited on (upgrade target / primary source). */
  source: string
  /** The source's last error, surfaced as a tooltip; omitted when empty. */
  reason?: string
}>()

const { now } = useNow()

// Live "~Nm" / "~Ns" / "~Nh" until the source's cooldown elapses (recomputes each tick).
const eta = computed(() => formatRetryEta(props.deferredUntil, now.value))
</script>

<template>
  <span class="defer" :title="reason || undefined">
    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M5 22h14M5 2h14M17 22v-4.17a2 2 0 0 0-.59-1.42L12 12l-4.41 4.41A2 2 0 0 0 7 17.83V22M7 2v4.17a2 2 0 0 0 .59 1.42L12 12l4.41-4.41A2 2 0 0 0 17 6.17V2" />
    </svg>
    waiting on <span class="defer__source">{{ source }}</span> · retry {{ eta }}
  </span>
</template>

<style scoped>
/* A muted, non-alarming pill: the chapter is not failed, only politely deferred.
   Token-only so both themes read. */
.defer {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: var(--space-2xs);
  font-size: 0.65625rem; /* 10.5px @16 — matches the sibling upgrade-tag step */
  font-weight: var(--weight-bold);
  padding: var(--space-3xs) var(--space-xs);
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
  white-space: nowrap;
}

/* The waited-on source is the load-bearing word — lift it out of the muted run. */
.defer__source {
  color: var(--text);
  font-weight: var(--weight-extrabold);
}
</style>
