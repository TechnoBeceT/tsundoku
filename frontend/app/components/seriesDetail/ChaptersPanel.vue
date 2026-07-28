<script setup lang="ts">
import { computed, ref } from 'vue'
import PanelCard from './PanelCard.vue'
import ChapterRow from './ChapterRow.vue'
import IconButton from '../ui/IconButton.vue'
import type { Chapter } from '../screens/seriesDetail.types'

/**
 * ChaptersPanel — the Series-Detail "Chapters" card: a titled header with an
 * asc/desc direction toggle + the total-count pill over a scrolling list of
 * `ChapterRow`s. The chapter list arrives ALREADY sorted latest-first
 * (descending) from the screen (`SeriesDetail.sortedChapters`); the panel
 * forwards each row's `read` (open in the reader), `set-current` (QCAT-242
 * "Set as current progress", carries the chapter NUMBER) and `redownload`
 * (QCAT-343, carries the chapter UUID) up to the screen.
 *
 * `redownloadingId` is the §16 in-flight marker for the re-download: the panel
 * matches it against each row's id so exactly the row being re-downloaded shows
 * its busy state, and every other row stays interactive.
 *
 * The direction toggle is Komikku-parity, local UI-only state (not persisted):
 * it defaults to DESCENDING (latest on top — the incoming order) and flipping
 * it to ascending simply REVERSES the displayed list in memory (the chevron
 * `IconButton` mirrors the library toolbar's direction control). It never
 * refetches or re-emits — a pure presentation flip.
 *
 * Wraps the shared PanelCard shell (divided header + full-bleed body); the
 * direction toggle + count pill ride the header-right `actions` slot. PanelCard
 * itself owns the scroll (`.panel__content`); this panel passes the QCAT-265
 * treatment #1 `max-height="580px"` bound (the prototype's own value) so the
 * long chapter list scrolls INTERNALLY while the page grows — the
 * asymmetric-pair case the owner ratified (§2.6.2, "chapters and sources
 * require inner scrolling").
 */
const props = withDefaults(defineProps<{
  /** The chapters to list, sorted latest-first (descending) upstream. */
  chapters: Chapter[]
  /** Total chapter count shown in the header pill. */
  total: number
  /** The chapter UUID whose re-download is in flight, or null when none is. */
  redownloadingId?: string | null
}>(), {
  redownloadingId: null,
})

const emit = defineEmits<{
  /** A chapter row's "Read" was clicked (carries the chapter UUID). */
  read: [chapterId: string]
  /** A chapter row's "Set as current progress" was clicked (carries the chapter NUMBER). */
  'set-current': [chapterNumber: number]
  /** A chapter row's re-download was clicked (carries the chapter UUID). */
  redownload: [chapterId: string]
}>()

// Local display direction — defaults to DESCENDING (the incoming order,
// latest-first). Ascending reverses the already-sorted list in memory.
const dir = ref<'asc' | 'desc'>('desc')
const displayedChapters = computed<Chapter[]>(() =>
  dir.value === 'desc' ? props.chapters : [...props.chapters].reverse(),
)
const dirLabel = computed(() => (dir.value === 'asc' ? 'Ascending' : 'Descending'))
const dirIcon = computed(() => (dir.value === 'asc' ? 'lucide:arrow-up-narrow-wide' : 'lucide:arrow-down-wide-narrow'))
function toggleDir(): void {
  dir.value = dir.value === 'asc' ? 'desc' : 'asc'
}
</script>

<template>
  <PanelCard title="Chapters" max-height="580px">
    <template #actions>
      <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
      <IconButton size="md" :ariaLabel="`Chapter order: ${dirLabel} (toggle)`" @click="toggleDir">
        <Icon :name="dirIcon" />
      </IconButton>
      <span class="count-pill">{{ total }}</span>
    </template>
    <ChapterRow
      v-for="ch in displayedChapters"
      :key="ch.chapterKey"
      :chapter="ch"
      :redownloading="redownloadingId === ch.id"
      @read="emit('read', $event)"
      @set-current="emit('set-current', $event)"
      @redownload="emit('redownload', $event)"
    />
  </PanelCard>
</template>

<style scoped>
.count-pill {
  padding: 1px var(--space-xs);
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}
</style>
