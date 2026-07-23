<script setup lang="ts">
import CoverImage from '../ui/CoverImage.vue'
import AppButton from '../ui/AppButton.vue'
import type { SeriesSourceless } from '../screens/sourceless.types'

/**
 * SourcelessSeriesCard — one series on the library Sourceless page.
 *
 * Simpler than `FractionalSeriesCard`: there is no ignore-policy toggle (a
 * sourceless chapter has no remaining carrier to flag) and no dual count — just
 * the cover/title/category identity strip and a single "N sourceless chapters"
 * count. The one action is "Review", which the parent screen turns into
 * `fetchPreview` + opening `SourcelessCleanupDialog`.
 *
 * Presentation-only: the row arrives via props and the action is emitted; the
 * parent owns the fetch/dialog. `busy` covers both the preview fetch and the
 * in-flight removal for THIS series (only one dialog is open at a time), so the
 * button spins and blocks re-clicks rather than opening a second flow. Token-only
 * colours → both themes render.
 */
const props = defineProps<{
  /** The series row to render. */
  row: SeriesSourceless
  /** This series' review/removal flow is in flight — spins + blocks the button. */
  busy: boolean
}>()

const emit = defineEmits<{
  /** "Review" clicked — the parent fetches the removable preview and opens the dialog. */
  review: [seriesId: string]
}>()
</script>

<template>
  <div class="scard">
    <div class="scard__head">
      <span class="scard__cover">
        <CoverImage :src="row.coverUrl" :alt="row.displayName" placeholder="initial" aspect="0.777" />
      </span>
      <span class="scard__titles">
        <span class="scard__title">{{ row.displayName }}</span>
        <span class="scard__cat">{{ row.category }}</span>
      </span>
    </div>

    <div class="scard__actions">
      <span class="scount">
        <span class="scount__value">{{ row.sourcelessCount }}</span>
        <span class="scount__label">sourceless chapter{{ row.sourcelessCount === 1 ? '' : 's' }}</span>
      </span>
      <AppButton
        variant="solid"
        size="sm"
        :loading="busy"
        :disabled="busy"
        @click="emit('review', props.row.seriesId)"
      >
        Review
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.scard {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-xl);
  padding: var(--space-lg);
  display: flex;
  flex-direction: column;
  gap: var(--space-base);
}

.scard__head {
  display: flex;
  align-items: center;
  gap: 0.8125rem;
  width: 100%;
}

.scard__cover {
  width: 2.625rem;
  border-radius: var(--radius-sm);
  overflow: hidden;
  flex: none;
}

.scard__titles {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-3xs);
}

.scard__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.scard__cat {
  font-size: var(--text-xs);
  color: var(--faint);
}

/* ---- Count + action --------------------------------------------------------- */
.scard__actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-base);
  flex-wrap: wrap;
}

.scount {
  display: flex;
  align-items: baseline;
  gap: var(--space-2xs);
  padding: var(--space-2xs) var(--space-sm);
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--surface2);
}

.scount__value {
  font-size: var(--text-md);
  font-weight: var(--weight-extrabold);
  color: var(--text);
}

.scount__label {
  font-size: var(--text-xs);
  color: var(--muted);
}
</style>
