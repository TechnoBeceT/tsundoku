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
 * the chapter meta line (which names the chapter's source — and, while it is
 * upgrading, the source it is converging TO: "MangaDex → Asura Scans"), and the
 * chapter-state badge. The variant-specific
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

// The source label. An empty providerName means NO source carries this chapter —
// nothing is fetching it — so we show an em-dash rather than a dangling separator.
// It reads as "no source", which is exactly the truth and makes a sourceless-stuck
// chapter visible instead of falsely crediting the series' top source.
const providerLabel = computed(() => props.item.providerName || '—')
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
      <div class="dl-row__meta">
        {{ metaLine }}
        <span class="dl-row__provider">
          · {{ providerLabel }}
          <template v-if="item.upgradeTarget">
            <span class="dl-row__arrow" aria-hidden="true">→</span>
            <span class="dl-row__target">{{ item.upgradeTarget }}</span>
          </template>
        </span>
      </div>
    </button>

    <!-- Grouped so the mobile breakpoint can drop the WHOLE trailing cluster to
         its own line under the title/meta (mirrors seriesDetail/ChapterRow's
         `.chapter__controls` fix) regardless of which slot content is present
         per tab (progress bar / upgrade tag / retry-count+next-attempt+retry
         button) — a plain flex-wrap on the individual siblings can't guarantee
         that grouping since the slot contents vary per caller. -->
    <div class="dl-row__controls">
      <slot name="before-badge" />
      <StatusBadge :state="item.state" />
      <slot name="after-badge" />
    </div>
  </div>
</template>

<style scoped>
.dl-row {
  display: flex;
  align-items: center;
  gap: 0.8125rem; /* 13px @16 — off-ladder, byte-identical rem literal */
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 0.6875rem var(--space-base); /* 11px 14px @16 (11px off-ladder) */
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
  width: 2.5rem; /* 40px @16 — byte-identical rem literal */
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
  gap: var(--space-xs);
}

.dl-row__title {
  font-weight: var(--weight-bold);
  font-size: 0.84375rem; /* 13.5px @16 — off-ladder, byte-identical rem literal */
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dl-row__meta {
  font-size: var(--text-sm);
  color: var(--muted);
  margin-top: var(--space-3xs);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dl-row__provider {
  color: var(--faint);
}

/* An upgrading row reads "<current> → <target>": the arrow + target stay inline in
   the dense meta line, with the TARGET emphasised — it is the source the chapter is
   converging to, which is the thing the owner is watching during a convergence wave. */
.dl-row__arrow {
  margin: 0 0.0625rem; /* 1px @16 — byte-identical rem literal */
}

.dl-row__target {
  color: var(--accent);
  font-weight: var(--weight-medium);
}

/* The trailing cluster (before-badge slot + status badge + after-badge slot) —
   flex:none on desktop, matching the individual siblings it replaces. */
.dl-row__controls {
  display: flex;
  align-items: center;
  gap: 0.8125rem; /* 13px @16 — off-ladder, byte-identical rem literal */
  flex: none;
}

@media (max-width: 900px) {
  /* `.dl-row`'s flex:none trailing cluster (progress bar / retry-count /
     status badge / retry button, depending on the caller) used to crowd the
     fixed width a phone has, crushing `.dl-row__info`'s flex:1 down to near
     nothing so the title had no room even for its own ellipsis. Wrapping the
     row and forcing `.dl-row__controls` onto its own full-width line (indented
     to align under the title, past the 40px cover + 13px gap) gives the cover
     + title the whole row on line 1, and drops the trailing cluster to line 2
     — nothing gets crushed, nothing overflows horizontally. */
  .dl-row {
    flex-wrap: wrap;
  }

  .dl-row__controls {
    /* 53px = cover (2.5rem) + row gap (0.8125rem), as rem so the indent tracks
       the cover+gap as the root scales — keeps line 2 aligned under the title. */
    flex: 1 1 calc(100% - 3.3125rem);
    margin-left: 3.3125rem;
    justify-content: flex-start;
    /* Defensive: Failed rows can pack retry-badge + next-attempt + status
       badge + a retry button into this cluster — let it wrap onto a further
       line rather than overflow on the narrowest phones. */
    flex-wrap: wrap;
  }
}
</style>
