<script setup lang="ts">
import ChapterRow from './ChapterRow.vue'
import type { Chapter } from '../screens/seriesDetail.types'

/**
 * ChaptersPanel — the Series-Detail "Chapters" card: a titled header with the
 * total-count pill over a scrolling list of `ChapterRow`s. Presentation-only —
 * the (already-sorted) chapter list and the total arrive via props.
 */
defineProps<{
  /** The chapters to list, in the order they should appear (sorted upstream). */
  chapters: Chapter[]
  /** Total chapter count shown in the header pill. */
  total: number
}>()
</script>

<template>
  <section class="panel">
    <div class="panel__head">
      <span class="panel__title">Chapters</span>
      <span class="count-pill">{{ total }}</span>
    </div>
    <div class="panel__scroll">
      <ChapterRow v-for="ch in chapters" :key="ch.chapterKey" :chapter="ch" />
    </div>
  </section>
</template>

<style scoped>
.panel {
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
  overflow: hidden;
  min-width: 0;
}

.panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 9px;
  padding: 15px 18px;
  border-bottom: 1px solid var(--border);
}

.panel__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: 15px;
  color: var(--text);
}

.count-pill {
  padding: 1px 8px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}

.panel__scroll {
  max-height: 580px;
  overflow: auto;
}
</style>
