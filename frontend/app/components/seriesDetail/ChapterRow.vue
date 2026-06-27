<script setup lang="ts">
import StatusBadge from '../ui/StatusBadge.vue'
import type { Chapter } from '../screens/seriesDetail.types'

/**
 * ChapterRow — one row in the Series-Detail chapter table: the (display) number,
 * the resolved chapter name with its CBZ filename beneath, an optional page-count,
 * and a `StatusBadge` for the download state. Presentation-only: the chapter
 * arrives via the `chapter` prop and the row emits nothing.
 *
 * The state badge reads the unified `--state-*` palette (via `StatusBadge`), so
 * every chapter-state hue across the app comes from one source and both themes work.
 */
const props = defineProps<{
  /** The chapter to render (identity is `chapterKey`, not the number). */
  chapter: Chapter
}>()

// Display name: provider title, else "Chapter N", else an em-dash placeholder.
const name = (): string =>
  props.chapter.name || (props.chapter.number != null ? `Chapter ${props.chapter.number}` : '—')
// Display number, or "—" when unknown (number is display/sort only, never identity).
const number = (): string => (props.chapter.number == null ? '—' : String(props.chapter.number))
// Page count badge, only once the CBZ is on disk (else empty → hidden).
const pages = (): string => (props.chapter.pageCount == null ? '' : `${props.chapter.pageCount}p`)
</script>

<template>
  <div class="chapter">
    <div class="chapter__num">{{ number() }}</div>
    <div class="chapter__main">
      <div class="chapter__name">{{ name() }}</div>
      <div v-if="chapter.filename" class="chapter__file">{{ chapter.filename }}</div>
    </div>
    <span v-if="pages()" class="chapter__pages">{{ pages() }}</span>
    <StatusBadge :state="chapter.state" />
  </div>
</template>

<style scoped>
.chapter {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 11px 18px;
  border-bottom: 1px solid var(--border);
}

.chapter__num {
  width: 40px;
  flex: none;
  font-family: var(--font-mono);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

.chapter__main {
  flex: 1;
  min-width: 0;
}

.chapter__name {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 13.5px;
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.chapter__file {
  margin-top: 2px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-family: var(--font-mono);
  font-size: 10.5px;
  color: var(--faint);
}

.chapter__pages {
  flex: none;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--faint);
}
</style>
