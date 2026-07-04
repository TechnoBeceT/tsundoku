<script setup lang="ts">
import { computed } from 'vue'
import Chip from '../ui/Chip.vue'
import CoverImage from '../ui/CoverImage.vue'
import StatusBadge from '../ui/StatusBadge.vue'
import type { DownloadItem } from '../screens/downloads.types'

/**
 * ChapterDownloadRow — THE shared download-activity row, used by all three
 * Downloads tabs (Active · Failed · Queued). It renders the parts every row has
 * in common: the clickable cover thumbnail, the series title + category chip,
 * the chapter meta line, and the chapter-state badge. The variant-specific
 * trailing content is injected by the parent:
 *   - `#before-badge` — sits between the meta and the badge (the Active progress
 *     bar, the Queued "UPGRADE" tag, a Failed row's retry-count + next-attempt).
 *   - `#after-badge`  — sits after the badge (a Failed row's retry/reset button).
 *
 * `bare` drops the row's own card surface so it can nest inside
 * `FailedDownloadCard` (which supplies the card chrome itself).
 *
 * Presentation only: the cover and title both emit `open-series` with the row's
 * series id; the parent owns navigation.
 */
const props = defineProps<{
  /** The chapter-activity item this row renders. */
  item: DownloadItem
  /** Drop the standalone card surface (when nested inside a card wrapper). */
  bare?: boolean
}>()

const emit = defineEmits<{
  /** The row (cover or title) was clicked — open that series. */
  'open-series': [seriesId: string]
}>()

// "#147" when the chapter number is known, else "" (dropped from the meta line).
const numberLabel = computed(() => (props.item.number == null ? '' : `#${props.item.number}`))

// The meta line under the title: "#147 · Chapter 147" (number dropped when null).
const metaLine = computed(() => [numberLabel.value, props.item.name].filter(Boolean).join(' · '))
</script>

<template>
  <div class="dl-row" :class="{ 'dl-row--bare': bare }">
    <CoverImage
      class="dl-row__cover"
      :src="item.coverUrl"
      :alt="`${item.seriesTitle} cover`"
      clickable
      :mark-size="18"
      radius="var(--radius-xs)"
      aspect="40 / 54"
      @click="emit('open-series', item.seriesId)"
    />

    <button type="button" class="dl-row__info" @click="emit('open-series', item.seriesId)">
      <div class="dl-row__titleline">
        <span class="dl-row__title">{{ item.seriesTitle }}</span>
        <Chip variant="category">{{ item.seriesCategory }}</Chip>
      </div>
      <div class="dl-row__meta">{{ metaLine }} <span class="dl-row__provider">· {{ item.providerName }}</span></div>
    </button>

    <slot name="before-badge" />
    <StatusBadge :state="item.state" />
    <slot name="after-badge" />
  </div>
</template>

<style scoped>
.dl-row {
  display: flex;
  align-items: center;
  gap: 13px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 11px 14px;
}

/* Nested inside a card (FailedDownloadCard) — the card owns the surface. */
.dl-row--bare {
  background: none;
  border: none;
  border-radius: 0;
  padding: 0;
}

/* The 40×54 thumb is a <CoverImage clickable>; it owns the surface, radius,
   placeholder, and lazy <img>. This wrapper only fixes the box width — the
   atom derives the 54px height from `aspect="40 / 54"`. */
.dl-row__cover {
  width: 40px;
  flex: none;
}

.dl-row__info {
  flex: 1;
  min-width: 0;
  text-align: left;
  padding: 0;
  border: none;
  background: none;
  cursor: pointer;
}

.dl-row__titleline {
  display: flex;
  align-items: center;
  gap: 8px;
}

.dl-row__title {
  font-weight: var(--weight-bold);
  font-size: 13.5px;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dl-row__meta {
  font-size: var(--text-sm);
  color: var(--muted);
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dl-row__provider {
  color: var(--faint);
}
</style>
