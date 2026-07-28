<script setup lang="ts">
import CoverImage from '../ui/CoverImage.vue'
import AppButton from '../ui/AppButton.vue'
import { formatBytes } from '~/utils/fileSize'
import type { SeriesDuplicateFiles } from '../screens/duplicates.types'

/**
 * DuplicateSeriesCard — one series on the Cleanup console's Duplicates tab.
 *
 * Shaped like `SourcelessSeriesCard` (identity strip + count + one action) with
 * two differences that follow from the tab being DISCOVERY ONLY:
 *   - it shows BOTH the file count and the reclaimable size, because "how many"
 *     and "how much disk" rank series differently — a hundred tiny leftovers can
 *     be worth less than three large ones;
 *   - its action OPENS THE SERIES instead of deleting anything. There is no
 *     library-wide removal endpoint by design; the removal is the owner-triggered
 *     "Remove duplicate files" button on the series page.
 *
 * Presentation-only: the row arrives via props and the action is emitted.
 * Token-only colours → both themes render.
 */
const props = defineProps<{
  /** The series row to render. */
  row: SeriesDuplicateFiles
}>()

const emit = defineEmits<{
  /** "Open series" clicked — the parent navigates to that series' detail page. */
  open: [seriesId: string]
}>()
</script>

<template>
  <div class="dcard">
    <div class="dcard__head">
      <span class="dcard__cover">
        <CoverImage :src="row.coverUrl" :alt="row.displayName" placeholder="initial" aspect="0.777" />
      </span>
      <span class="dcard__titles">
        <span class="dcard__title">{{ row.displayName }}</span>
        <span class="dcard__cat">{{ row.category }}</span>
      </span>
    </div>

    <div class="dcard__actions">
      <span class="dcount">
        <span class="dcount__value">{{ row.fileCount }}</span>
        <span class="dcount__label">duplicate file{{ row.fileCount === 1 ? '' : 's' }}</span>
        <span class="dcount__size">{{ formatBytes(row.reclaimableBytes) }}</span>
      </span>
      <AppButton variant="solid" size="sm" @click="emit('open', props.row.seriesId)">
        Open series
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.dcard {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-xl);
  padding: var(--space-lg);
  display: flex;
  flex-direction: column;
  gap: var(--space-base);
}

.dcard__head {
  display: flex;
  align-items: center;
  gap: 0.8125rem;
  width: 100%;
}

.dcard__cover {
  width: 2.625rem;
  border-radius: var(--radius-sm);
  overflow: hidden;
  flex: none;
}

.dcard__titles {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-3xs);
}

.dcard__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dcard__cat {
  font-size: var(--text-xs);
  color: var(--faint);
}

/* ---- Count + action --------------------------------------------------------- */
.dcard__actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-base);
  flex-wrap: wrap;
}

.dcount {
  display: flex;
  align-items: baseline;
  gap: var(--space-2xs);
  padding: var(--space-2xs) var(--space-sm);
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--surface2);
}

.dcount__value {
  font-size: var(--text-md);
  font-weight: var(--weight-extrabold);
  color: var(--text);
}

.dcount__label {
  font-size: var(--text-xs);
  color: var(--muted);
}

.dcount__size {
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--text);
  padding-left: var(--space-2xs);
  border-left: 1px solid var(--border);
}
</style>
