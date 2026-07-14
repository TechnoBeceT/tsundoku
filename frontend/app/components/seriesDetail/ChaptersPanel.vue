<script setup lang="ts">
import PanelCard from './PanelCard.vue'
import ChapterRow from './ChapterRow.vue'
import type { Chapter } from '../screens/seriesDetail.types'

/**
 * ChaptersPanel — the Series-Detail "Chapters" card: a titled header with the
 * total-count pill over a scrolling list of `ChapterRow`s. The (already-sorted)
 * chapter list and the total arrive via props; the panel forwards each row's
 * `read` (open in the reader) and `set-current` (QCAT-242 "Set as current
 * progress", carries the chapter NUMBER) up to the screen. Wraps the shared
 * PanelCard shell (divided header + full-bleed body); the count pill rides
 * the header-right `actions` slot. PanelCard itself owns the scroll
 * (`.panel__content`) — this panel does not set its own overflow/max-height.
 */
defineProps<{
  /** The chapters to list, in the order they should appear (sorted upstream). */
  chapters: Chapter[]
  /** Total chapter count shown in the header pill. */
  total: number
}>()

const emit = defineEmits<{
  /** A chapter row's "Read" was clicked (carries the chapter UUID). */
  read: [chapterId: string]
  /** A chapter row's "Set as current progress" was clicked (carries the chapter NUMBER). */
  'set-current': [chapterNumber: number]
}>()
</script>

<template>
  <PanelCard title="Chapters">
    <template #actions>
      <span class="count-pill">{{ total }}</span>
    </template>
    <ChapterRow
      v-for="ch in chapters"
      :key="ch.chapterKey"
      :chapter="ch"
      @read="emit('read', $event)"
      @set-current="emit('set-current', $event)"
    />
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
</style>
