<script setup lang="ts">
import type { ChapterInspect } from '../screens/import.types'

/**
 * ChapterInspectList — the resolved chapter-inspect preview shown under a
 * <CandidateConfigRow> once a candidate's chapters arrive (§16 round-trip): a
 * count headline + a scrollable grid of "Ch. <number> · <name>" rows.
 * Presentation-only — the chapters arrive via the `chapters` prop.
 */
defineProps<{
  /** The chapter-preview rows for the inspected candidate. */
  chapters: ChapterInspect[]
}>()

// Chapter-row label: "Ch. <number> · <name>", with graceful gaps for a missing
// number (—) or an empty name (number only).
const chapterLabel = (ch: ChapterInspect): string => {
  const num = ch.number == null ? '—' : String(ch.number)
  return ch.name ? `Ch. ${num} · ${ch.name}` : `Ch. ${num}`
}
</script>

<template>
  <div class="cil">
    <p class="cil__count">{{ chapters.length }} chapters available</p>
    <ul class="cil__list">
      <li v-for="(ch, ci) in chapters" :key="ci" class="cil__item">
        {{ chapterLabel(ch) }}
      </li>
    </ul>
  </div>
</template>

<style scoped>
.cil {
  margin-top: 0.6875rem; /* 11px @16 — off-ladder, byte-identical rem literal */
  padding: 0.6875rem 0.8125rem; /* 11px 13px @16 — off-ladder, byte-identical */
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--surface);
}

.cil__count {
  margin: 0 0 var(--space-xs); /* 8px @16 */
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--accentBright);
}

/* A fixed-max-height chapter-preview scroller (a small, deliberately bounded
 * inner list under the row — the prototype's own inner-scroll shape, NOT a
 * viewport-keyed letterbox; §2.6 treatment #1). max-height on rem so the box
 * scales with the fluid root. */
.cil__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(12.5rem, 1fr)); /* 200px @16 */
  gap: var(--space-2xs) var(--space-base); /* 4px 14px @16 */
  max-height: 10.5rem; /* 168px @16 */
  overflow-y: auto;
}

.cil__item {
  font-size: var(--text-sm);
  color: var(--muted);
  line-height: 1.5;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
