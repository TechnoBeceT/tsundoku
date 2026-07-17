<script setup lang="ts">
import type { TabItem } from './nav.types'

/**
 * SegmentedTabs — a count-pill tab bar. Each tab is a bordered button; the
 * active one is painted in the soft-accent fill, the rest stay quiet. An
 * optional `count` renders as a small mono pill after the label (e.g. how many
 * series sit under a category, or how many downloads are failed).
 *
 * Used for the LibraryList category filter, the Downloads main + failed
 * sub-tabs, and the Settings extension tabs. NOTE: this is distinct from
 * `SegmentedToggle`, which is the small *filled* 2-way switch — different look,
 * different job.
 *
 * `tabs` is the ordered list of `{ key, label, count? }`; `modelValue` is the
 * active tab `key` (v-model). Emits `update:modelValue` with the picked `key`.
 * Built as an accessible `role="tablist"` of real `role="tab"` buttons.
 */
defineProps<{
  /** The active tab key (v-model). */
  modelValue: string
  /** The ordered tabs to render. */
  tabs: TabItem[]
}>()

const emit = defineEmits<{
  /** A tab was picked — carries its key. */
  'update:modelValue': [value: string]
}>()
</script>

<template>
  <div class="tabs" role="tablist">
    <button
      v-for="t in tabs"
      :key="t.key"
      type="button"
      role="tab"
      class="tab"
      :class="{ 'tab--active': modelValue === t.key }"
      :aria-selected="modelValue === t.key"
      @click="emit('update:modelValue', t.key)"
    >
      {{ t.label }}
      <span
        v-if="t.count !== undefined"
        class="tab__count"
        :class="{ 'tab__count--active': modelValue === t.key }"
      >{{ t.count }}</span>
    </button>
  </div>
</template>

<style scoped>
.tabs {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-xs);
}

.tab {
  display: flex;
  align-items: center;
  gap: 0.4375rem; /* 7px @16 — off-ladder, byte-identical (not rounded to 6px) */
  padding: var(--space-xs) var(--space-base); /* 8px 14px @16 */
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.tab:hover {
  color: var(--text);
  border-color: var(--border2);
}

.tab--active {
  border-color: transparent;
  background: var(--accentSoft);
  color: var(--accentBright);
}

.tab:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.tab__count {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  /* 1px is a deliberate hairline vertical inset (the count pill must not grow the
   * tab's line box); the 7px inline padding rides the scale as byte-identical rem. */
  padding: 1px 0.4375rem; /* 1px 7px @16 */
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--faint);
}

.tab__count--active {
  background: var(--accent);
  color: var(--cover-text);
}
</style>
