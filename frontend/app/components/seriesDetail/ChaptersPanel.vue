<script setup lang="ts">
import PanelCard from './PanelCard.vue'
import ChapterRow from './ChapterRow.vue'
import type { Chapter } from '../screens/seriesDetail.types'

/**
 * ChaptersPanel — the Series-Detail "Chapters" card: a titled header with the
 * total-count pill over a scrolling list of `ChapterRow`s. Presentation-only —
 * the (already-sorted) chapter list and the total arrive via props. Wraps the
 * shared PanelCard shell (divided header + full-bleed body); the count pill rides
 * the header-right `actions` slot and the scroll list is the full-bleed body.
 */
defineProps<{
  /** The chapters to list, in the order they should appear (sorted upstream). */
  chapters: Chapter[]
  /** Total chapter count shown in the header pill. */
  total: number
}>()
</script>

<template>
  <PanelCard title="Chapters">
    <template #actions>
      <span class="count-pill">{{ total }}</span>
    </template>
    <div class="panel__scroll">
      <ChapterRow v-for="ch in chapters" :key="ch.chapterKey" :chapter="ch" />
    </div>
  </PanelCard>
</template>

<style scoped>
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
