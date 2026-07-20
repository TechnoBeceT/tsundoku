<script setup lang="ts">
import StatusBadge from '../ui/StatusBadge.vue'
import AppButton from '../ui/AppButton.vue'
import IconButton from '../ui/IconButton.vue'
import type { Chapter } from '../screens/seriesDetail.types'
import { isReadableState } from '~/utils/readableStates'
import { relativeTime, absoluteTime } from '~/utils/timeFormat'

/**
 * ChapterRow — one row in the Series-Detail chapter table: the (display) number,
 * the resolved chapter name with its CBZ filename beneath, an optional page-count,
 * a "Read" button for on-disk chapters, a "Set as current progress" action
 * (QCAT-242), and a `StatusBadge` for the download state. The chapter arrives
 * via the `chapter` prop; the row emits `read` (the chapter UUID) when the
 * owner opens it in the reader, and `set-current` (the chapter's NUMBER) when
 * the owner picks "Set as current progress".
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
 *
 * "Set as current progress" (QCAT-242) needs a real chapter NUMBER (the
 * backend target is a plain number, not an id) — it only renders when
 * `chapter.number` is known, and is destructive (marks later chapters unread,
 * jumps every bound tracker), so the row only EMITS the request; the actual
 * confirm + mutation live upstream (the page owns `SetChapterProgressDialog`,
 * mirroring the Remove-source confirm dialog's page-owned pattern).
 */
const props = defineProps<{
  /** The chapter to render (identity is `chapterKey`, not the number). */
  chapter: Chapter
}>()

const emit = defineEmits<{
  /** Open this chapter in the reader (carries the chapter UUID). */
  read: [chapterId: string]
  /** "Set as current progress" was picked — carries the chapter's display NUMBER. */
  'set-current': [chapterNumber: number]
}>()

// Guards the emit: the button itself is v-if'd on a known number, but the
// handler re-checks so a null number can never slip a NaN target upstream.
function onSetCurrent(): void {
  if (props.chapter.number != null) emit('set-current', props.chapter.number)
}

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

// Release date (QCAT-297): a short "3d ago" relative label, with the full local
// timestamp on hover (both from the shared timeFormat util). Empty for a chapter
// no source dated that was never downloaded (releaseDate === null).
const released = (): string => (props.chapter.releaseDate == null ? '' : relativeTime(props.chapter.releaseDate))
const releasedTitle = (): string => absoluteTime(props.chapter.releaseDate)
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
    <!-- Grouped so the mobile breakpoint can drop the WHOLE cluster to its own
         line under the name (see .chapter__controls below) regardless of
         which of the two optional members (page count / Read button)
         render — a plain flex-wrap on the individual siblings can't guarantee
         that grouping since which items are even present varies per row. -->
    <div class="chapter__controls">
      <span v-if="released()" class="chapter__released" :title="releasedTitle()">{{ released() }}</span>
      <span v-if="pages()" class="chapter__pages">{{ pages() }}</span>
      <AppButton
        v-if="isReadableState(chapter.state)"
        variant="mini"
        size="sm"
        @click="emit('read', chapter.id)"
      >
        Read
      </AppButton>
      <!-- eslint-disable vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
      <IconButton
        v-if="chapter.number != null"
        size="sm"
        :ariaLabel="`Set chapter ${number()} as current progress`"
        @click="onSetCurrent"
      >
        <Icon name="lucide:target" width="14" height="14" />
      </IconButton>
      <!-- eslint-enable vue/attribute-hyphenation -->
      <StatusBadge :state="chapter.state" />
    </div>
  </div>
</template>

<style scoped>
/* Off-ladder raw px in visible properties migrated to exact spacing tokens /
 * byte-identical rem (value÷16) — design px at the 16px desktop anchor, fluid
 * on a phone. */
.chapter {
  display: flex;
  align-items: center;
  gap: var(--space-md); /* 12px */
  padding: 0.6875rem var(--space-xl); /* 11px 18px */
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
  gap: 0.3125rem; /* 5px */
  width: 2.5rem; /* 40px */
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
  width: 0.375rem; /* 6px */
  height: 0.375rem; /* 6px */
  flex-shrink: 0;
  border-radius: var(--radius-pill);
  background: var(--accent);
}

.chapter__main {
  flex: 1;
  min-width: 0;
}

.chapter__resume {
  margin-top: 0.125rem; /* 2px */
  font-family: var(--font-mono);
  font-size: 0.65625rem; /* 10.5px */
  font-weight: var(--weight-bold);
  color: var(--accentBright);
}

.chapter__name {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 0.84375rem; /* 13.5px */
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.chapter__file {
  margin-top: 0.125rem; /* 2px */
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-family: var(--font-mono);
  font-size: 0.65625rem; /* 10.5px */
  color: var(--faint);
}

.chapter__pages {
  flex: none;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--faint);
}

/* Release date — a subtle "3d ago" marker (absolute date on hover). Muted so it
 * reads as row metadata, not a control. */
.chapter__released {
  flex: none;
  font-size: var(--text-xs);
  color: var(--faint);
  white-space: nowrap;
}

/* The page-count / Read / status-badge cluster — flex:none on desktop, same
 * as the individual siblings it replaces (12px gap, matching `.chapter`'s
 * own gap so the desktop row is pixel-identical to before). */
.chapter__controls {
  display: flex;
  align-items: center;
  gap: var(--space-md); /* 12px */
  flex: none;
}

@media (max-width: 900px) {
  /* The row's `flex:none` controls (page-count/Read/badge) used to eat the
   * fixed width a phone has, crushing `.chapter__main`'s `flex:1` down to
   * near-nothing so the chapter name had no room to even show its ellipsis.
   * Wrapping `.chapter` and forcing `.chapter__controls` onto its own
   * full-width line (flex-basis 100%) gives the number + name the WHOLE row
   * width on line 1, and drops the controls to line 2 — nothing shrinks or
   * gets hidden, nothing overflows. */
  .chapter {
    flex-wrap: wrap;
  }

  .chapter__controls {
    /* basis nets out to exactly (row width - the indent) so the outer box
     * including `margin-left` never exceeds the row's content width —
     * `flex: 1 0 100%` + a separate margin would overflow by the margin
     * amount instead. flex-shrink:1 (the "1" in the shorthand) is a safety
     * margin for subpixel rounding, not load-bearing on its own. The 3.25rem
     * indent (52px @16 = num-cell 2.5rem + gap 0.75rem) is rem so it stays
     * aligned under the name as the fluid root shrinks both on a phone. */
    flex: 1 1 calc(100% - 3.25rem);
    /* Align under the name, not the number gutter. */
    margin-left: 3.25rem; /* 52px */
    justify-content: flex-start;
    /* Defensive: on the narrowest phones a long badge label ("Failed ·
     * final") beside a page-count can still be tight — let the cluster
     * itself wrap rather than overflow horizontally. */
    flex-wrap: wrap;
    /* IconButton's mobile 44px hit overlay overhangs the sub-44px "Set as
     * current progress" square; keep a `--touch-pitch` gap (raw px — a finger
     * is physical) so the overlay can't spill onto the adjacent "Read" button
     * and land an ambiguous tap on the (confirm-guarded) progress reset (§7). */
    column-gap: var(--touch-pitch);
  }
}
</style>
