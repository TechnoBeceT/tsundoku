<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Chip from '../ui/Chip.vue'
import CoverImage from '../ui/CoverImage.vue'
import Dialog from '../ui/Dialog.vue'
import EmptyState from '../ui/EmptyState.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import Skeleton from '../ui/Skeleton.vue'
import type { CoverCandidate } from '../screens/seriesDetail.types'

/**
 * CoverPickerModal — the "Choose cover" gallery, composed on the wide-gallery
 * Dialog shell (Komga's "change poster" feel). In Tsundoku the COVER is chosen
 * INDEPENDENTLY of the metadata match: the owner picks the poster from ANY
 * provider — a tracker (AniList / MAL), a metadata provider (MangaDex /
 * MangaUpdates), or a scraped source ("Asura Scans"). This is a per-field
 * `cover_source` choice, SEPARATE from the Identify modal's whole-series match.
 *
 * COVER-FIRST, unlike the text-match Identify grid: this is about the ART, so the
 * tiles are larger portrait posters (`minmax(150px, …)`) with only a small
 * provider label under each. ONE view, no step machine:
 *   - a grid of candidate tiles when `candidates` is non-empty — each a portrait
 *     CoverImage + a provider Chip; single-select via a local `selectedId`
 *     (accent ring + a check badge on the picked tile), and the tile matching
 *     `currentId` carries a subtle "Current" marker;
 *   - a skeleton cover grid while `loading`;
 *   - the "No covers found" empty state when a fetch returned nothing.
 *   - footer: Cancel (ghost) + "Use this cover" (primary, disabled until a
 *     candidate is picked → emits `confirm`).
 *
 * Presentation-only: the parent owns the fetch and passes `candidates` +
 * `currentId` + `loading` down; this modal renders them and emits the owner's
 * intent (`confirm` / `cancel`). Opening resets the selection to `currentId`
 * (or none), mirroring the other Series-Detail dialogs' reset-on-open.
 *
 * `error` (optional, mirrors RemoveSourceDialog/MatchSourceDialog/…): a failed
 * gallery load or cover pick surfaces here via `ErrorBanner`, §16 — the owner
 * never confirms into the void behind the modal's own overlay.
 */
const props = withDefaults(defineProps<{
  /** Whether the modal is shown (v-model:open). */
  open: boolean
  /** The candidate covers to offer in the gallery. */
  candidates: CoverCandidate[]
  /** The id of the cover the series currently uses — marked "Current" + preselected. */
  currentId?: string
  /** True while covers are being fetched — shows the skeleton grid. */
  loading?: boolean
  /** A failed load/pick message to show inside the modal, or null when there is none. */
  error?: string | null
}>(), {
  currentId: undefined,
  loading: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** A cover was confirmed as the new poster. */
  'confirm': [candidate: CoverCandidate]
  /** The modal was dismissed without confirming. */
  'cancel': []
}>()

// Initialised from props so a story mounted already-open shows the preselection
// immediately; the watch re-primes on every subsequent open.
const selectedId = ref<string | null>(props.currentId ?? null)

watch(() => props.open, (isOpen) => {
  if (isOpen) selectedId.value = props.currentId ?? null
})

const selected = computed<CoverCandidate | null>(
  () => props.candidates.find((c) => c.id === selectedId.value) ?? null,
)

// Empty state: not loading and no candidates to show.
const showEmpty = computed(() => !props.loading && props.candidates.length === 0)

function confirmCover() {
  if (selected.value) emit('confirm', selected.value)
}
</script>

<template>
  <Dialog
    :open="open"
    title="Choose cover"
    max-width="800px"
    @update:open="emit('update:open', $event)"
    @close="emit('cancel')"
  >
    <div class="picker">
      <p class="picker__lead">
        Pick a poster from any provider — a tracker, a metadata provider, or a source. The cover is chosen independently of the metadata match.
      </p>

      <ErrorBanner v-if="error" class="picker__error" :message="error" :dismissible="false" />

      <!-- loading: a skeleton grid in the same shape as the results -->
      <div v-if="loading" class="picker__grid" aria-hidden="true">
        <div v-for="n in 8" :key="n" class="picker__skel">
          <Skeleton variant="cover" />
          <Skeleton variant="line" height="11px" />
        </div>
      </div>

      <!-- empty: a fetch that returned no covers -->
      <EmptyState
        v-else-if="showEmpty"
        title="No covers found"
        sub="No provider offered a cover for this series. Try again later."
      >
        <template #icon><Icon name="lucide:image-off" /></template>
      </EmptyState>

      <!-- grid: the candidate covers, single-select -->
      <div v-else class="picker__grid">
        <button
          v-for="c in candidates"
          :key="c.id"
          type="button"
          class="tile"
          :class="{ 'tile--selected': c.id === selectedId }"
          :aria-pressed="c.id === selectedId"
          @click="selectedId = c.id"
        >
          <span class="tile__cover">
            <CoverImage
              :src="c.coverUrl"
              :alt="`${c.provider} cover`"
              placeholder="initial"
              :initial="c.provider"
              radius="var(--radius-md)"
            />
            <span v-if="c.id === selectedId" class="tile__check" aria-hidden="true">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M20 6 9 17l-5-5" />
              </svg>
            </span>
            <span v-if="c.id === currentId" class="tile__current">Current</span>
          </span>

          <span class="tile__label">
            <Chip :variant="c.id === selectedId ? 'accent' : 'neutral'">{{ c.provider }}</Chip>
          </span>
        </button>
      </div>
    </div>

    <template #actions>
      <AppButton variant="ghost" size="md" @click="emit('update:open', false)">
        Cancel
      </AppButton>
      <AppButton
        variant="primary"
        size="md"
        :disabled="!selected"
        @click="confirmCover"
      >
        <template #icon><Icon name="lucide:image" /></template>
        Use this cover
      </AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.picker {
  display: flex;
  flex-direction: column;
}

.picker__lead {
  margin: 0 0 18px;
  font-size: var(--text-base);
  line-height: 1.5;
  color: var(--muted);
}

.picker__error {
  margin-bottom: 16px;
}

/* ---- Grid ----------------------------------------------------------------- */
/* Cover-first: larger portrait posters than the Identify grid — this is about
   the art. The wide 800px gallery fits ~4 covers/row. */
.picker__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 14px;
}

.picker__skel {
  display: flex;
  flex-direction: column;
  gap: 9px;
}

/* ---- Tile ----------------------------------------------------------------- */
.tile {
  display: flex;
  flex-direction: column;
  gap: 9px;
  padding: 8px;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--border);
  background: var(--surface2);
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s, box-shadow 0.15s;
}

.tile:hover {
  border-color: var(--border2);
}

.tile--selected {
  border-color: var(--accent);
  background: var(--accentSoft);
  box-shadow: 0 0 0 3px var(--accentSoft);
}

.tile:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

/* ---- Cover ---------------------------------------------------------------- */
.tile__cover {
  position: relative;
  display: block;
}

/* The accent check badge, top-right on the cover, for the selected tile. */
.tile__check {
  position: absolute;
  top: 6px;
  right: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: var(--accent);
  color: var(--cover-text);
  box-shadow: var(--shadow-accent-sm);
}

/* The "Current" marker — a frosted pill bottom-left on the cover, so the poster
   the series already uses is legible at a glance without stealing the tile. */
.tile__current {
  position: absolute;
  left: 6px;
  bottom: 6px;
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  background: var(--cover-frost);
  backdrop-filter: blur(4px);
  color: var(--cover-text);
  font-size: 10px;
  font-weight: var(--weight-bold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
}

/* ---- Label ---------------------------------------------------------------- */
.tile__label {
  display: flex;
  min-width: 0;
}
</style>
