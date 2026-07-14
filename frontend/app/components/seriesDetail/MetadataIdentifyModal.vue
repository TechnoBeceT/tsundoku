<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Dialog from '../ui/Dialog.vue'
import EmptyState from '../ui/EmptyState.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import Skeleton from '../ui/Skeleton.vue'
import TextField from '../ui/TextField.vue'
import MetadataCandidateCard from './MetadataCandidateCard.vue'
import type { MetadataCandidate } from '../screens/seriesDetail.types'

/**
 * MetadataIdentifyModal — the manual-correction "Identify" match tool, composed on
 * the wide-gallery Dialog shell. Auto-identify already runs in the BACKGROUND on
 * import / add-series (elsewhere); this modal is the MANUAL override the owner
 * opens when that automatic match is wrong — or to MERGE several correct matches
 * (QCAT-228: the metadata engine unions collections + gap-fills scalars across
 * every picked provider) instead of replacing the series' data with just one.
 *
 * ONE view, no step machine:
 *   - a header search row — a "Title" field prefilled from the series title
 *     (editable, Enter searches) with an inline primary Search button;
 *   - directly below, the candidate area: a MULTI-select grid of
 *     MetadataCandidateCard when `candidates` is non-empty (any number of cards
 *     may be picked; each shows its 1-based PICK ORDER — pick 1 is the primary/
 *     anchor for the merge), a skeleton grid while `loading`, or the "No matches
 *     found" empty state when a search returned nothing;
 *   - footer: Cancel (ghost) + a Confirm button (primary, disabled until at
 *     least one candidate is picked) whose label reflects the pick count.
 *
 * Presentation-only: the parent owns the search/fetch and passes `candidates` +
 * `loading` down; this modal renders them and emits the owner's intent
 * (`search` / `confirm` / `cancel`). `confirm` carries the picks IN PICK ORDER —
 * the parent sends them as `selections` (selections[0] = primary). Opening
 * re-primes the title field and drops any selection (mirrors the other
 * Series-Detail dialogs' reset-on-open).
 *
 * `error` (optional, mirrors RemoveSourceDialog/MatchSourceDialog/…): a failed
 * search or confirm surfaces here via `ErrorBanner`, §16 — the owner never
 * searches/confirms into the void behind the modal's own overlay.
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
  /** A failed search/confirm message to show inside the modal, or null when there is none. */
  error?: string | null
}>(), {
  loading: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** A search was requested — carries the trimmed query. */
  'search': [query: string]
  /** One or more candidates were confirmed as the merge picks, in pick order (index 0 = primary). */
  'confirm': [candidates: MetadataCandidate[]]
  /** The modal was dismissed without confirming. */
  'cancel': []
}>()

// Initialised from props so a story mounted already-open shows the prefilled
// field + candidates immediately; the watch re-primes on every subsequent open.
const query = ref(props.title)
// Picked candidate ids, in PICK ORDER (push on select, splice on deselect) —
// index 0 is always the primary/anchor, regardless of grid position.
const selectedIds = ref<string[]>([])

watch(() => props.open, (isOpen) => {
  if (isOpen) {
    query.value = props.title
    selectedIds.value = []
  }
})

const trimmedQuery = computed(() => query.value.trim())

/** The picked candidates, resolved in PICK ORDER (index 0 = primary). */
const selected = computed<MetadataCandidate[]>(() =>
  selectedIds.value
    .map((id) => props.candidates.find((c) => c.id === id))
    .filter((c): c is MetadataCandidate => c !== undefined),
)

/** rank(id) → this candidate's 1-based pick order, or undefined when unpicked. */
function rankOf(id: string): number | undefined {
  const i = selectedIds.value.indexOf(id)
  return i === -1 ? undefined : i + 1
}

/** Toggles a candidate's pick — add to the end (new lowest priority) if unpicked, remove if picked. */
function toggle(id: string) {
  const i = selectedIds.value.indexOf(id)
  if (i === -1) selectedIds.value.push(id)
  else selectedIds.value.splice(i, 1)
}

// Empty state: not loading and no candidates to show.
const showEmpty = computed(() => !props.loading && props.candidates.length === 0)

/** Confirm-button label — reflects the pick count so the merge intent is visible. */
const confirmLabel = computed(() => {
  const n = selected.value.length
  if (n <= 1) return 'Confirm match'
  return `Merge ${n} matches`
})

function runSearch() {
  if (!trimmedQuery.value) return
  emit('search', trimmedQuery.value)
}

function confirmMatch() {
  if (selected.value.length > 0) emit('confirm', selected.value)
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
        Search a metadata provider and pick one or more correct matches — picks merge into the series'
        synopsis, tags, and links (the first pick is primary). A merge locks the series' metadata so it
        won't be silently overwritten by auto-identify.
      </p>

      <ErrorBanner v-if="error" class="identify__error" :message="error" :dismissible="false" />

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

      <!-- grid: the candidate results, MULTI-select — any number of cards may be
           picked, each showing its 1-based pick order via `rank`. -->
      <div v-else class="identify__grid">
        <MetadataCandidateCard
          v-for="c in candidates"
          :key="c.id"
          :candidate="c"
          :selected="selectedIds.includes(c.id)"
          :rank="rankOf(c.id)"
          @select="toggle(c.id)"
        />
      </div>
    </div>

    <template #actions>
      <span v-if="selected.length > 0" class="identify__picked" aria-live="polite">
        {{ selected.length }} picked — primary: {{ selected[0]?.provider }}
      </span>
      <AppButton variant="ghost" size="md" @click="emit('update:open', false)">
        Cancel
      </AppButton>
      <AppButton
        variant="primary"
        size="md"
        :disabled="selected.length === 0"
        @click="confirmMatch"
      >
        {{ confirmLabel }}
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

.identify__error {
  margin-bottom: 16px;
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

/* ---- Footer pick summary --------------------------------------------------- */
/* Sits in the same flex row as the action buttons (Dialog's #actions slot is
   justify-content:flex-end); margin-right:auto pushes it to the left edge so
   the buttons stay right-aligned. */
.identify__picked {
  margin-right: auto;
  align-self: center;
  font-size: var(--text-sm);
  color: var(--muted);
}
</style>
