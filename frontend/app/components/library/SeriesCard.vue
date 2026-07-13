<script setup lang="ts">
import { computed } from 'vue'
import CoverImage from '../ui/CoverImage.vue'
import Chip from '../ui/Chip.vue'
import Tag from '../ui/Tag.vue'
import ProgressBar from '../ui/ProgressBar.vue'
import type { SeriesSummary } from '../screens/types'

/**
 * SeriesCard — one cover-forward card in the library grid: a portrait cover (or a
 * branded placeholder when there's no `coverUrl`), an on-cover category badge and
 * status flags (PAUSED when un-monitored, DONE when completed, NEEDS SOURCE when
 * the series has no live download source — cover-independent, see
 * `series.needsSource`), and a bottom overlay with the title, a
 * download-progress bar, and the chapter-count meta.
 *
 * Presentation only: all data arrives via the `series` prop and the click is
 * emitted as `select` — no fetching, routing, or stores. It composes the shared
 * atoms (`CoverImage`, `Chip`, `Tag`, `ProgressBar`) and references only design
 * tokens, so it renders correctly in both themes.
 */
const props = defineProps<{
  /** The series summary to render (cover, category, flags, chapter tallies). */
  series: SeriesSummary
}>()

const emit = defineEmits<{
  /** The card was clicked — carries the series id. */
  select: [seriesId: string]
}>()

// Download progress as a whole percent (0 when there are no chapters yet).
const progressPct = computed(() => {
  const c = props.series.chapterCounts
  return c.total > 0 ? Math.round((c.downloaded / c.total) * 100) : 0
})
</script>

<template>
  <button
    type="button"
    class="card"
    :aria-label="series.title"
    @click="emit('select', series.id)"
  >
    <!-- Cover image, or a branded placeholder when coverUrl is empty -->
    <CoverImage
      class="card__cover"
      :src="series.coverUrl"
      :alt="`${series.title} cover`"
      aspect="1 / 1.38"
    />

    <!-- Scrim keeps overlaid text legible over any cover -->
    <div class="card__scrim" />

    <!-- Top row: category badge (left) + unread count / status flags (right corner) -->
    <div class="card__top">
      <Chip variant="frost">{{ series.category }}</Chip>
      <div class="card__flags">
        <!-- Unread badge: downloaded-but-unread chapters — what can be read
             RIGHT NOW, deliberately not the source's full known-chapter count.
             Hidden at zero: the badge's presence IS the signal, a wall of 0s
             is worse than no badge. Additive to (not a replacement for) the
             download-progress bar in the card body below. -->
        <div v-if="series.chapterCounts.unread > 0" class="card__unread">
          {{ series.chapterCounts.unread }}
        </div>
        <Tag v-if="!series.monitored" tone="frost">
          <template #icon>
            <svg width="9" height="9" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <rect x="6" y="5" width="4" height="14" rx="1" />
              <rect x="14" y="5" width="4" height="14" rx="1" />
            </svg>
          </template>
          PAUSED
        </Tag>
        <Tag v-if="series.completed" tone="success">
          <template #icon>
            <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M20 6L9 17l-5-5" />
            </svg>
          </template>
          DONE
        </Tag>
        <!-- Needs source: a live-source-independent signal (handover 2026-07-13#15)
             — deliberately part of the on-cover overlay, not gated on coverUrl, so
             it renders EVEN WHEN the series has a metadata cover. -->
        <Tag v-if="series.needsSource" tone="warn">
          <template #icon>
            <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M12 9v4M12 17h.01M10.29 3.86l-8.18 14.18A2 2 0 0 0 3.82 21h16.36a2 2 0 0 0 1.71-2.96L13.71 3.86a2 2 0 0 0-3.42 0z" />
            </svg>
          </template>
          NEEDS SOURCE
        </Tag>
      </div>
    </div>

    <!-- Bottom: title, progress bar, count meta -->
    <div class="card__body">
      <div class="card__title">{{ series.title }}</div>
      <ProgressBar
        class="card__bar"
        :value="progressPct"
        track="var(--cover-track)"
        tone="var(--accentBright)"
      />
      <div class="card__meta">
        <span class="card__downloaded">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <path d="M7 10l5 5 5-5" />
            <path d="M12 15V3" />
          </svg>
          {{ series.chapterCounts.downloaded }}/{{ series.chapterCounts.total }}
        </span>
        <span v-if="series.chapterCounts.wanted > 0" class="card__wanted">{{ series.chapterCounts.wanted }} wanted</span>
        <span v-if="series.chapterCounts.failed > 0" class="card__failed">{{ series.chapterCounts.failed }} failed</span>
      </div>
    </div>
  </button>
</template>

<style scoped>
.card {
  position: relative;
  display: block;
  width: 100%;
  padding: 0;
  text-align: left;
  cursor: pointer;
  border-radius: 15px;
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--border);
  transition: transform 0.16s, border-color 0.16s, box-shadow 0.16s;
}

.card:hover {
  transform: translateY(-5px);
  border-color: var(--border2);
  box-shadow: var(--shadow);
}

.card:focus-visible {
  outline: none;
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

/* The CoverImage box sets the card's portrait footprint; the button's 15px
   overflow-clip rounds the corners, so the cover's own radius is dropped. */
.card__cover {
  border-radius: 0;
}

.card__scrim {
  position: absolute;
  inset: 0;
  background: var(--cover-scrim);
}

.card__top {
  position: absolute;
  top: 9px;
  left: 9px;
  right: 9px;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 6px;
}

.card__flags {
  display: flex;
  flex-direction: column;
  gap: 5px;
  align-items: flex-end;
}

.card__unread {
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  height: 20px;
  padding: 0 6px;
  border-radius: 10px;
  background: var(--accentBright);
  color: var(--cover-text);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1;
}

.card__body {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  padding: 11px;
}

.card__title {
  font-weight: var(--weight-bold);
  font-size: 13.5px;
  color: var(--cover-text);
  line-height: 1.22;
  margin-bottom: 8px;
  min-height: 33px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.card__bar {
  margin-bottom: 7px;
}

.card__meta {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--cover-text-soft);
}

.card__downloaded {
  display: flex;
  align-items: center;
  gap: 4px;
}

.card__wanted {
  color: var(--cover-text-soft);
}

.card__failed {
  color: var(--cover-fail);
}
</style>
