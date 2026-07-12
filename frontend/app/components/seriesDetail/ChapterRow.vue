<script setup lang="ts">
import StatusBadge from '../ui/StatusBadge.vue'
import AppButton from '../ui/AppButton.vue'
import type { Chapter } from '../screens/seriesDetail.types'

/**
 * ChapterRow — one row in the Series-Detail chapter table: the (display) number,
 * the resolved chapter name with its CBZ filename beneath, an optional page-count,
 * a "Read" button for on-disk chapters, and a `StatusBadge` for the download state.
 * The chapter arrives via the `chapter` prop; the row emits `read` (the chapter
 * UUID) when the owner opens it in the reader.
 *
 * The state badge reads the unified `--state-*` palette (via `StatusBadge`), so
 * every chapter-state hue across the app comes from one source and both themes work.
 *
 * In-app reader progress renders as exactly ONE of three mutually-exclusive
 * states (Task 7 — the data already arrived on `Chapter.read`/`.lastReadPage`,
 * it was just being dropped before this):
 *   - read       → the row dims (opacity), no unread dot.
 *   - unread     → never opened (`lastReadPage === 0`) — full-strength row + a
 *                  small unread dot next to the chapter number.
 *   - partially read (`lastReadPage > 0 && !read`) → a "Page N / M" resume line
 *                  under the chapter name. `lastReadPage` is 0-BASED but the
 *                  line displays 1-based (page index 17 → "Page 18").
 */
const props = defineProps<{
  /** The chapter to render (identity is `chapterKey`, not the number). */
  chapter: Chapter
}>()

const emit = defineEmits<{
  /** Open this chapter in the reader (carries the chapter UUID). */
  read: [chapterId: string]
}>()

// Display name: provider title, else "Chapter N", else an em-dash placeholder.
const name = (): string =>
  props.chapter.name || (props.chapter.number != null ? `Chapter ${props.chapter.number}` : '—')
// Display number, or "—" when unknown (number is display/sort only, never identity).
const number = (): string => (props.chapter.number == null ? '—' : String(props.chapter.number))
// Page count badge, only once the CBZ is on disk (else empty → hidden).
const pages = (): string => (props.chapter.pageCount == null ? '' : `${props.chapter.pageCount}p`)

// Never opened: no unread dot once there's ANY progress (partially read or
// finished) — the dot means "hasn't been touched at all".
const isUnread = (): boolean => !props.chapter.read && props.chapter.lastReadPage === 0
const isPartiallyRead = (): boolean => !props.chapter.read && props.chapter.lastReadPage > 0
// 1-based display of the 0-based lastReadPage; denominator omitted when the
// page count isn't known (shouldn't happen for a partially-read chapter, but
// pageCount is nullable on the type).
const resumeLine = (): string => {
  const shown = props.chapter.lastReadPage + 1
  return props.chapter.pageCount == null ? `Page ${shown}` : `Page ${shown} / ${props.chapter.pageCount}`
}
</script>

<template>
  <div class="chapter" :class="{ 'chapter--read': chapter.read }">
    <div class="chapter__num-cell">
      <span class="chapter__num">{{ number() }}</span>
      <span v-if="isUnread()" class="chapter__dot" aria-hidden="true" />
    </div>
    <div class="chapter__main">
      <div class="chapter__name">{{ name() }}</div>
      <div v-if="chapter.filename" class="chapter__file">{{ chapter.filename }}</div>
      <div v-if="isPartiallyRead()" class="chapter__resume">{{ resumeLine() }}</div>
    </div>
    <span v-if="pages()" class="chapter__pages">{{ pages() }}</span>
    <AppButton
      v-if="chapter.state === 'downloaded'"
      variant="mini"
      size="sm"
      @click="emit('read', chapter.id)"
    >
      Read
    </AppButton>
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
  transition: opacity 0.15s;
}

/* Read chapters dim relative to unread/partially-read ones (no unread dot;
 * see .chapter__dot below). */
.chapter--read {
  opacity: 0.6;
}

.chapter__num-cell {
  display: flex;
  align-items: center;
  gap: 5px;
  width: 40px;
  flex: none;
}

.chapter__num {
  font-family: var(--font-mono);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

/* Unread indicator — a chapter that has never been opened at all (distinct
 * from "partially read", which shows the resume line instead). */
.chapter__dot {
  width: 6px;
  height: 6px;
  flex-shrink: 0;
  border-radius: var(--radius-pill);
  background: var(--accent);
}

.chapter__main {
  flex: 1;
  min-width: 0;
}

.chapter__resume {
  margin-top: 2px;
  font-family: var(--font-mono);
  font-size: 10.5px;
  font-weight: var(--weight-bold);
  color: var(--accentBright);
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
