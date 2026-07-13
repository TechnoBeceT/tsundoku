<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Dialog from '../ui/Dialog.vue'
import EmptyState from '../ui/EmptyState.vue'
import Skeleton from '../ui/Skeleton.vue'
import TextField from '../ui/TextField.vue'
import MetadataCandidateCard from './MetadataCandidateCard.vue'
import type { MetadataCandidate } from '../screens/seriesDetail.types'

/**
 * MetadataIdentifyModal — the manual-correction "Identify" match tool, composed on
 * the wide-gallery Dialog shell. Auto-identify already runs in the BACKGROUND on
 * import / add-series (elsewhere); this modal is the MANUAL override the owner
 * opens when that automatic match is wrong.
 *
 * ONE view, no step machine:
 *   - a header search row — a "Title" field prefilled from the series title
 *     (editable, Enter searches) with an inline primary Search button;
 *   - directly below, the candidate area: a single-select grid of
 *     MetadataCandidateCard when `candidates` is non-empty, a skeleton grid while
 *     `loading`, or the "No matches found" empty state when a search returned
 *     nothing.
 *   - footer: Cancel (ghost) + Confirm match (primary, disabled until a candidate
 *     is picked).
 *
 * Presentation-only: the parent owns the search/fetch and passes `candidates` +
 * `loading` down; this modal renders them and emits the owner's intent
 * (`search` / `confirm` / `cancel`). Opening re-primes the title field and drops
 * any selection (mirrors the other Series-Detail dialogs' reset-on-open).
 */
const props = withDefaults(defineProps<{
  /** Whether the modal is shown (v-model:open). */
  open: boolean
  /** The series title — prefills the search field on open. */
  title: string
  /** The search results to offer in the candidate grid. */
  candidates: MetadataCandidate[]
  /** True while a search is in flight — shows the skeleton grid. */
  loading?: boolean
}>(), {
  loading: false,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** A search was requested — carries the trimmed query. */
  'search': [query: string]
  /** A candidate was confirmed as the match. */
  'confirm': [candidate: MetadataCandidate]
  /** The modal was dismissed without confirming. */
  'cancel': []
}>()

// Initialised from props so a story mounted already-open shows the prefilled
// field + candidates immediately; the watch re-primes on every subsequent open.
const query = ref(props.title)
const selectedId = ref<string | null>(null)

watch(() => props.open, (isOpen) => {
  if (isOpen) {
    query.value = props.title
    selectedId.value = null
  }
})

const trimmedQuery = computed(() => query.value.trim())

const selected = computed<MetadataCandidate | null>(
  () => props.candidates.find((c) => c.id === selectedId.value) ?? null,
)

// Empty state: not loading and no candidates to show.
const showEmpty = computed(() => !props.loading && props.candidates.length === 0)

function runSearch() {
  if (!trimmedQuery.value) return
  emit('search', trimmedQuery.value)
}

function confirmMatch() {
  if (selected.value) emit('confirm', selected.value)
}
</script>

<template>
  <Dialog
    :open="open"
    title="Identify"
    max-width="800px"
    @update:open="emit('update:open', $event)"
    @close="emit('cancel')"
  >
    <div class="identify">
      <p class="identify__lead">
        Search a metadata provider and pick the correct match to pull a synopsis, tags, and a fresh cover.
      </p>

      <!-- Header search row: editable Title field + inline Search button. -->
      <div class="identify__searchrow">
        <div class="identify__field">
          <TextField
            v-model="query"
            label="Title"
            placeholder="Series title"
            @enter="runSearch"
          />
        </div>
        <AppButton
          variant="primary"
          size="md"
          :disabled="!trimmedQuery"
          @click="runSearch"
        >
          <template #icon><Icon name="lucide:search" /></template>
          Search
        </AppButton>
      </div>

      <!-- loading: a skeleton grid in the same shape as the results -->
      <div v-if="loading" class="identify__grid" aria-hidden="true">
        <div v-for="n in 8" :key="n" class="identify__skel">
          <Skeleton variant="cover" />
          <Skeleton variant="line" height="11px" />
        </div>
      </div>

      <!-- empty: a search that matched nothing -->
      <EmptyState
        v-else-if="showEmpty"
        title="No matches found"
        sub="Try a different title or spelling, then search again."
      >
        <template #icon><Icon name="lucide:search-x" /></template>
      </EmptyState>

      <!-- grid: the candidate results, single-select -->
      <div v-else class="identify__grid">
        <MetadataCandidateCard
          v-for="c in candidates"
          :key="c.id"
          :candidate="c"
          :selected="c.id === selectedId"
          @select="selectedId = c.id"
        />
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
        @click="confirmMatch"
      >
        Confirm match
      </AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.identify {
  display: flex;
  flex-direction: column;
}

.identify__lead {
  margin: 0 0 16px;
  font-size: var(--text-base);
  line-height: 1.5;
  color: var(--muted);
}

/* Search field + inline button; the button sits on the input's baseline (the
   field carries a label above, so align to the bottom edge). */
.identify__searchrow {
  display: flex;
  align-items: flex-end;
  gap: 10px;
  margin-bottom: 18px;
}

.identify__field {
  flex: 1;
  min-width: 0;
}

/* ---- Grid ----------------------------------------------------------------- */
/* Larger tiles than the standard dialog — the wide gallery fits ~4 covers/row. */
.identify__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 14px;
}

.identify__skel {
  display: flex;
  flex-direction: column;
  gap: 9px;
}
</style>
