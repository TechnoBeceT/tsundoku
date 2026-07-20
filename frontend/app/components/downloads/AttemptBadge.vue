<script setup lang="ts">
import { computed } from 'vue'

/**
 * AttemptBadge — the per-source retry-budget pill: "‹source› · N/max".
 *
 * Surfaces `ProviderChapter.attempts` against `jobs.max_retries` for the source
 * actually fetching this chapter (the engine's PER-SOURCE budget — a chapter only
 * fails once EVERY source is exhausted, not at a single N). This is strictly more
 * information than Kaizoku's bare "Retry #N": the owner sees which source and how
 * close it is to being abandoned ("Asura Scans · 1/5").
 *
 * Tints toward danger as the budget is spent: neutral while fresh, warn once any
 * attempt has been made, danger when exhausted (attempts ≥ max). Presentation
 * only — the parent decides WHEN to render it (max > 0).
 */
const props = defineProps<{
  /** Display name of the source these attempts are against. */
  provider: string
  /** Attempts made so far against that source (ProviderChapter.attempts). */
  attempts: number
  /** The per-source retry budget (jobs.max_retries) — the denominator. */
  max: number
}>()

const tone = computed(() => {
  if (props.attempts >= props.max) return 'exhausted'
  if (props.attempts > 0) return 'trying'
  return 'fresh'
})
</script>

<template>
  <span class="attempts" :class="`attempts--${tone}`" :title="`${provider}: ${attempts} of ${max} attempts`">
    <span v-if="provider" class="attempts__src">{{ provider }}</span>
    <span v-if="provider" class="attempts__sep" aria-hidden="true">·</span>
    <span class="attempts__count">{{ attempts }}/{{ max }}</span>
  </span>
</template>

<style scoped>
.attempts {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: var(--space-3xs);
  max-width: 12rem;
  font-size: 0.65625rem; /* 10.5px @16 — matches the sibling upgrade-tag/defer step */
  font-weight: var(--weight-bold);
  padding: var(--space-3xs) var(--space-xs);
  border-radius: var(--radius-pill);
  white-space: nowrap;
}

.attempts__src {
  overflow: hidden;
  text-overflow: ellipsis;
  font-weight: var(--weight-semibold);
}

.attempts__count {
  flex: none;
  font-variant-numeric: tabular-nums;
}

/* Fresh: no attempt spent yet — the neutral queued pill. */
.attempts--fresh {
  background: var(--dl-queued-bg);
  color: var(--dl-queued-text);
}

/* Trying: at least one attempt spent — amber warning. */
.attempts--trying {
  background: var(--warn-bg, var(--dl-queued-bg));
  color: var(--warn);
}

/* Exhausted: budget spent against this source — danger. */
.attempts--exhausted {
  background: var(--dl-error-pill-bg);
  color: var(--dl-failed-text);
}
</style>
