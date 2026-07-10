<script setup lang="ts">
import { computed, ref, toRef, watch } from 'vue'
import Dialog from '../ui/Dialog.vue'
import AppButton from '../ui/AppButton.vue'
import SearchInput from '../ui/SearchInput.vue'
import Spinner from '../ui/Spinner.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import SearchGroupCard from '../import/SearchGroupCard.vue'
import AdoptTray from '../import/AdoptTray.vue'
import SourceConfigurePanel from '../import/SourceConfigurePanel.vue'
import { useSourceConfigure, type ProviderRef } from '~/composables/useSourceConfigure'
import type { ScanlatorCoverage, SearchCandidate, SearchGroup } from '../screens/import.types'

/**
 * MatchSourceDialog — the Series-Detail "Add a source" dialog: the inverse of
 * removing a source. Search by the series' OWN title (it's already imported —
 * this is NOT the Scan-Library path-based match step), gather one or more
 * cross-source candidates via the shared Adopt-wizard Configure powers
 * (multi-select tray, per-scanlator coverage, importance ranking), and attach
 * them all in one batch call.
 *
 * Rebuilt for Slice P (was single-select: one group, one candidate, one
 * priority number) onto the same `useSourceConfigure` composable +
 * `SourceConfigurePanel` that `Import.vue` uses — no reimplemented row,
 * tray, or split logic (§2 DRY). Title/category are NOT editable here: this
 * dialog only ADDS sources to a series that already exists, so the Configure
 * stage shows the series' own (fixed) title read-only.
 *
 * Presentation-only, mirroring `Import.vue`: all data arrives via props and
 * every network-touching action (`search`, `loadBreakdowns`, `confirm`) is
 * emitted for the parent's `useMatchSource` composable to run. The two-stage
 * flow (search → configure) is owned locally as a plain `stage` ref — the
 * composable itself does not own stage (mirrors `Import.vue`'s ownership
 * split, documented on `useSourceConfigure`).
 *
 *   - `open` (v-model:open): whether the dialog is shown.
 *   - `seriesTitle`: the series' own title — prefills the search box, shown
 *     read-only in the Configure stage, and restored every time the dialog
 *     re-opens.
 *   - `groups`: the current cross-source search results.
 *   - `breakdowns`: per-scanlator coverage cache, keyed `source:mangaId`
 *     (mirrors `Import.vue`'s prop of the same name) — drives the
 *     composable's auto-split of a source into per-scanlator rows.
 *   - `searching`: a search is in flight (spinner + disabled Search button).
 *   - `saving`: the batch-attach POST is in flight — spins + disables the
 *     confirm button and blocks the dialog from being dismissed (§16).
 *   - `error`: a search-or-attach failure message, or null for none.
 *
 * Resets its local flow state (query/stage) AND the composable's own tray +
 * picked group every time it opens, so a re-open never inherits a stale
 * gather/selection (mirrors `DeleteSeriesDialog`'s reset-on-open).
 *
 * Emits `update:open` (v-model), `search` (the trimmed query string),
 * `loadBreakdowns` (the picked/tray-configured candidates, for the parent to
 * fetch coverage), and `confirm` (the ordered, best-first `ProviderRef[]` to
 * attach).
 *
 * Tray-leak guard: `tray-enabled` is intentionally ON here (this surface is
 * MULTI-select) — the single-select match surfaces that reuse
 * `SearchGroupCard` (`scanLibrary/MatchPanel`, this dialog's sibling
 * `MatchDiskProviderDialog`) leave it off and are untouched by this change.
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** The series' own title — prefills the search box, shown read-only in Configure. */
  seriesTitle?: string
  /** The current cross-source search results. */
  groups?: SearchGroup[]
  /** Per-scanlator breakdown cache, keyed `source:mangaId` (see `useSourceConfigure`). */
  breakdowns?: Record<string, ScanlatorCoverage[] | null>
  /** A search is in flight. */
  searching?: boolean
  /** The batch-attach POST is in flight. */
  saving?: boolean
  /** A search-or-attach failure message, or null for none. */
  error?: string | null
}>(), {
  seriesTitle: '',
  groups: () => [],
  breakdowns: () => ({}),
  searching: false,
  saving: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** Run a search for the trimmed query. */
  'search': [q: string]
  /** Fetch the per-scanlator breakdown for every given candidate (Configure-stage entry). */
  'loadBreakdowns': [candidates: SearchCandidate[]]
  /** Attach the gathered, ranked sources — best-first. */
  'confirm': [providers: ProviderRef[]]
}>()

const query = ref(props.seriesTitle)
const stage = ref<'search' | 'configure'>('search')
const searched = ref(false)

// The Configure-stage orchestration (tray, row selection/order, per-scanlator
// split, rank) is owned by the shared composable (Slice P) — this dialog
// keeps only query/stage/searched (its own concerns).
const {
  tray,
  trayActive,
  isGroupAdded,
  addGroup,
  removeGroup,
  removeCand,
  configureTray,
  group,
  enterConfigure,
  displayRows,
  selectedCount,
  toggleCand,
  moveCand,
  orderedProviders,
  breakdownsResolving,
} = useSourceConfigure({
  breakdowns: toRef(props, 'breakdowns'),
  onLoadBreakdowns: c => emit('loadBreakdowns', c),
})

// Reset the whole flow every time the dialog opens — a re-open never
// inherits a stale query, tray, or picked group.
watch(() => props.open, (isOpen) => {
  if (isOpen) {
    query.value = props.seriesTitle
    stage.value = 'search'
    searched.value = false
    tray.value = []
    group.value = null
  }
})

const noResults = computed(() => searched.value && !props.searching && props.groups.length === 0)

function runSearch(): void {
  searched.value = true
  emit('search', query.value.trim())
}

// Classic single-group pick (tray empty) — advances straight to Configure.
function pickGroup(g: SearchGroup): void {
  enterConfigure(g.candidates)
  stage.value = 'configure'
}

// The tray's "Configure N sources →" — builds a synthetic group from every
// gathered candidate and advances to Configure the same way.
function onConfigureTray(): void {
  configureTray()
  stage.value = 'configure'
}

function back(): void {
  stage.value = 'search'
}

function onBackOrCancel(): void {
  if (stage.value === 'configure') back()
  else emit('update:open', false)
}

function confirm(): void {
  if (selectedCount.value === 0 || props.saving || breakdownsResolving.value) return
  emit('confirm', orderedProviders.value)
}
</script>

<template>
  <Dialog
    :open="open"
    :busy="saving"
    title="Add a source"
    @update:open="emit('update:open', $event)"
  >
    <ErrorBanner v-if="error" class="match__error" :message="error" :dismissible="false" />

    <!-- ============= Search stage ============= -->
    <section v-if="stage === 'search'" class="match-stage">
      <div class="match-search">
        <SearchInput
          v-model="query"
          class="match-search__field"
          :clearable="false"
          placeholder="Search a title across sources…"
          @enter="runSearch"
        />
        <AppButton variant="primary" :loading="searching" @click="runSearch">
          Search
        </AppButton>
      </div>

      <div v-if="searching" class="match-loading">
        <Spinner :size="16" tone="accent" />
        Searching sources…
      </div>
      <p v-else-if="noResults" class="match-note">No matches found. Try another title.</p>

      <!-- Cross-search gather tray: persists across a new search, always above the results -->
      <AdoptTray
        v-if="trayActive"
        :candidates="tray"
        @configure="onConfigureTray"
        @remove="removeCand"
      />

      <div v-if="!searching && groups.length" class="match-groups">
        <SearchGroupCard
          v-for="g in groups"
          :key="g.title"
          :group="g"
          tray-enabled
          :added="isGroupAdded(g)"
          :tray-active="trayActive"
          @pick="pickGroup"
          @add="addGroup"
          @remove="removeGroup"
        />
      </div>
    </section>

    <!-- ============= Configure stage ============= -->
    <section v-else class="match-stage">
      <p class="match-eyebrow">Adding sources to</p>
      <p class="match-series-title">{{ seriesTitle }}</p>

      <SourceConfigurePanel
        :rows="displayRows"
        hide-inspect
        label="Sources to attach · use arrows to rank priority"
        @toggle="toggleCand"
        @move="moveCand($event.key, $event.dir)"
      />
      <p v-if="breakdownsResolving" class="match-note match-note--loading">Loading coverage…</p>
    </section>

    <template #actions>
      <AppButton variant="ghost" :disabled="saving" @click="onBackOrCancel">
        {{ stage === 'configure' ? 'Back' : 'Cancel' }}
      </AppButton>
      <AppButton
        v-if="stage === 'configure'"
        variant="primary"
        :loading="saving"
        :disabled="selectedCount === 0 || saving || breakdownsResolving"
        @click="confirm"
      >
        Attach sources
      </AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.match__error {
  margin-bottom: 14px;
}

.match-stage {
  display: block;
}

.match-search {
  display: flex;
  gap: 10px;
  margin-bottom: 14px;
}

.match-search__field {
  flex: 1;
}

.match-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 30px 0;
  color: var(--muted);
  font-size: var(--text-base);
}

.match-note {
  margin: 0;
  padding: 26px 0;
  text-align: center;
  font-size: 13.5px;
  color: var(--muted);
}

.match-note--loading {
  padding: 10px 0 0;
  text-align: left;
  color: var(--faint);
}

.match-groups {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.match-eyebrow {
  margin: 0 0 3px;
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
}

.match-series-title {
  margin: 0 0 16px;
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: var(--text-xl);
  color: var(--text);
}
</style>
