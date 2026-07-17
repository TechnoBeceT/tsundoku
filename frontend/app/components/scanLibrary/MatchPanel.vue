<script setup lang="ts">
import { ref, toRef, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import Spinner from '../ui/Spinner.vue'
import SearchGroupCard from '../import/SearchGroupCard.vue'
import AdoptTray from '../import/AdoptTray.vue'
import SourceConfigurePanel from '../import/SourceConfigurePanel.vue'
import { useSourceConfigure, type ProviderRef } from '~/composables/useSourceConfigure'
import type { ScanlatorCoverage, SearchCandidate, SearchGroup } from '../screens/import.types'

/**
 * MatchPanel — the Scan Library "Match a source" sub-panel: attaches one or
 * more Suwayomi sources to a staged (disk-only) entry so future chapters/
 * upgrades work, instead of leaving it disk-only forever. Reuses the SAME
 * grouped candidates the Import/Adopt wizard renders (`GET
 * /api/library/imports/match` returns the identical `SearchGroup`/
 * `SearchCandidate` DTO as `GET /api/search`) and the SAME Configure-stage
 * powers (multi-select tray, per-scanlator coverage, importance ranking) the
 * Series-Detail "Add a source" dialog (`MatchSourceDialog`) uses, via the
 * shared `useSourceConfigure` composable + `SourceConfigurePanel` — this
 * panel never re-implements candidate rendering, tray, or split logic (§2 DRY
 * / §8 reuse).
 *
 * Rebuilt for Slice P (was single-select: one group, one candidate, a fixed
 * importance of 2, no coverage, no scanlator). The import call itself now
 * sends the resolved, best-first `ProviderRef[]` — no importance (the
 * backend assigns each strictly below the disk-origin provider's importance
 * of 1, decision E, in list order) — instead of the old single
 * `{source, mangaId, importance}`.
 *
 * Two internal stages, mirroring `MatchSourceDialog`'s search/configure split:
 *   1. **Groups** — pick which cross-source group is the right series
 *      (`SearchGroupCard`, tray-enabled — the owner can gather several
 *      groups' candidates from the ONE match search before configuring, or
 *      classic one-tap pick straight into Configure while the tray is empty).
 *   2. **Configure** — `SourceConfigurePanel` renders the gathered
 *      candidates' rows (auto-split by scanlator once a breakdown resolves,
 *      multi-select, re-rankable) from the shared `useSourceConfigure`
 *      composable's `displayRows`. There is no editable title/category here
 *      (unlike the Adopt wizard) — the panel header above already shows the
 *      staged entry's own (fixed) title.
 *
 * Presentation-only: the parent (`ScanLibrary.vue`) owns whether the panel is
 * shown at all and supplies the match search's own loading/error state
 * (`searching`/`searchError`, from `useScanLibrary().matching`/`.matchError`)
 * separately from the CONFIRM mutation's loading/error state (`busy`/`error`,
 * from `useScanLibrary().busy(path)`/`.error(path)`) — two distinct async
 * operations, two distinct §16 state pairs. `breakdowns` (per-scanlator
 * coverage cache, keyed `source:mangaId`) arrives the same way, and every
 * Configure-stage breakdown fetch is emitted via `loadBreakdowns` for the
 * parent's `useScanLibrary.loadBreakdowns` to run (§16 — no fetching here).
 *
 * Resets to the Groups stage (and drops the gathered tray + picked group)
 * whenever `groups` changes: a fresh match search's results (the owner
 * clicked Match on a staged entry; `groups` starts `[]` while `searching`,
 * then updates once the search resolves) must never leave a stale Configure
 * selection showing. Unlike a dialog's open/close, this panel has no single
 * "reopen" moment to reset on — the parent mounts it via `v-if` as soon as
 * the search STARTS (so it can show the spinner), then the SAME instance
 * re-renders once `groups` actually updates — hence the reset lives on a
 * `groups` watcher rather than an `open` watcher (see `ScanLibrary.vue`'s
 * `onMatch`).
 *
 * Tray-leak guard: `tray-enabled` is intentionally ON here (Slice P widened
 * this surface to MULTI-select) — the sibling single-select match surface
 * `MatchDiskProviderDialog` (the no-re-download link of an unlinked
 * disk-origin group) is untouched and still leaves it off.
 */
const props = withDefaults(defineProps<{
  /** The staged entry's title, for the panel header. */
  title: string
  /** Cross-source candidate groups returned by the match search. */
  groups: SearchGroup[]
  /** Per-scanlator breakdown cache, keyed `source:mangaId` (see `useSourceConfigure`). */
  breakdowns?: Record<string, ScanlatorCoverage[] | null>
  /** True while the match search itself is in flight. */
  searching?: boolean
  /** A match-search failure message, or "" for none. */
  searchError?: string
  /** True while the confirmed match is being imported (the CONFIRM mutation). */
  busy?: boolean
  /** The confirmed match's import failure, or "" for none. */
  error?: string
}>(), {
  breakdowns: () => ({}),
  searching: false,
  searchError: '',
  busy: false,
  error: '',
})

const emit = defineEmits<{
  /** The owner confirmed the gathered, ranked sources — best-first — to attach at import time. */
  confirm: [providers: ProviderRef[]]
  /** Abandon the match flow — the parent returns to the staging table. */
  back: []
  /** Fetch the per-scanlator breakdown for every given candidate (Configure-stage entry). */
  loadBreakdowns: [candidates: SearchCandidate[]]
}>()

const stage = ref<'groups' | 'configure'>('groups')

// The Configure-stage orchestration (tray, row selection/order, per-scanlator
// split, rank) is owned by the shared composable (Slice P) — this panel keeps
// only `stage` (its own concern; the composable does not own stage, mirroring
// `MatchSourceDialog`).
const cfg = useSourceConfigure({
  breakdowns: toRef(props, 'breakdowns'),
  onLoadBreakdowns: c => emit('loadBreakdowns', c),
})

// A fresh match search (new `groups` prop — the owner matched a different
// row, or this one's search just resolved) always restarts at the
// group-picking stage and drops any gathered tray/selection.
watch(() => props.groups, () => {
  stage.value = 'groups'
  cfg.tray.value = []
  cfg.group.value = null
})

// Classic single-group pick (tray empty) — advances straight to Configure.
function pickGroup(g: SearchGroup): void {
  cfg.enterConfigure(g.candidates)
  stage.value = 'configure'
}

// The tray's "Configure N sources →" — builds a synthetic group from every
// gathered candidate and advances to Configure the same way.
function onConfigureTray(): void {
  cfg.configureTray()
  stage.value = 'configure'
}

/** Stage-aware back: from Configure it returns to Groups; from Groups it exits the panel. */
function handleBack(): void {
  if (stage.value === 'configure') stage.value = 'groups'
  else emit('back')
}

function confirm(): void {
  if (cfg.selectedCount.value === 0 || props.busy || cfg.breakdownsResolving.value) return
  emit('confirm', cfg.orderedProviders.value)
}
</script>

<template>
  <div class="match-panel">
    <div class="mp-head">
      <span class="mp-eyebrow">Match a source</span>
      <span class="mp-title">{{ title }}</span>
    </div>

    <ErrorBanner v-if="error" class="mp-error" :message="error" :dismissible="false" />

    <div v-if="searching" class="mp-loading">
      <Spinner :size="16" tone="accent" />
      Searching sources…
    </div>

    <ErrorBanner v-else-if="searchError" class="mp-error" :message="searchError" :dismissible="false" />

    <p v-else-if="groups.length === 0" class="mp-note mp-note--center">
      No matches found across any source.
    </p>

    <template v-else-if="stage === 'groups'">
      <p class="mp-subhead">
        {{ groups.length }} possible match{{ groups.length === 1 ? '' : 'es' }} · choose one or gather several
      </p>

      <AdoptTray
        v-if="cfg.trayActive.value"
        :candidates="cfg.tray.value"
        @configure="onConfigureTray"
        @remove="cfg.removeCand"
      />

      <div class="mp-groups">
        <SearchGroupCard
          v-for="g in groups"
          :key="g.title"
          :group="g"
          tray-enabled
          :added="cfg.isGroupAdded(g)"
          :tray-active="cfg.trayActive.value"
          @pick="pickGroup"
          @add="cfg.addGroup"
          @remove="cfg.removeGroup"
        />
      </div>
      <div class="mp-actions mp-actions--start">
        <AppButton variant="ghost" @click="emit('back')">Back</AppButton>
      </div>
    </template>

    <template v-else>
      <SourceConfigurePanel
        :rows="cfg.displayRows.value"
        hide-inspect
        label="Sources to attach · use arrows to rank priority"
        @toggle="cfg.toggleCand"
        @move="cfg.moveCand($event.key, $event.dir)"
      />
      <div class="mp-actions mp-actions--between">
        <AppButton variant="ghost" :disabled="busy" @click="handleBack">Back</AppButton>
        <div class="mp-actions__end">
          <span v-if="cfg.breakdownsResolving.value" class="mp-note mp-note--loading">Loading coverage…</span>
          <AppButton
            variant="primary"
            :loading="busy"
            :disabled="cfg.selectedCount.value === 0 || busy || cfg.breakdownsResolving.value"
            @click="confirm"
          >
            Attach {{ cfg.selectedCount.value }} source{{ cfg.selectedCount.value === 1 ? '' : 's' }}
          </AppButton>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.match-panel {
  display: block;
}

.mp-head {
  display: flex;
  flex-direction: column;
  gap: 0.1875rem; /* 3px @16 */
  margin-bottom: var(--space-xl); /* 18px @16 */
}

.mp-eyebrow {
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
}

.mp-title {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: var(--text-2xl);
  color: var(--text);
}

.mp-error {
  margin-bottom: var(--space-lg); /* 16px @16 */
}

.mp-subhead {
  margin: 0 0 0.6875rem; /* 11px @16 */
  font-size: var(--text-sm);
  color: var(--muted);
  font-weight: var(--weight-semibold);
}

.mp-groups {
  display: flex;
  flex-direction: column;
  gap: var(--space-md); /* 12px @16 */
}

.mp-note {
  margin: 0;
  font-size: 0.84375rem; /* 13.5px @16 */
  color: var(--muted);
}

.mp-note--center {
  padding: 2.125rem 0; /* 34px @16 */
  text-align: center;
}

.mp-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-sm); /* 10px @16 */
  padding: 2.5rem 0; /* 40px @16 */
  color: var(--muted);
  font-size: var(--text-base);
}

.mp-actions {
  display: flex;
  margin-top: var(--space-2xl-tight); /* 20px @16 */
}

.mp-actions--start {
  justify-content: flex-start;
}

.mp-actions--between {
  justify-content: space-between;
}

.mp-actions__end {
  display: flex;
  align-items: center;
  gap: var(--space-sm); /* 10px @16 */
}

.mp-note--loading {
  color: var(--faint);
}

@media (max-width: 900px) {
  /* The Configure-stage footer (Back · loading note · Attach N sources) can't
   * share one line at phone width — wrap it instead of overflowing. */
  .mp-actions--between {
    flex-wrap: wrap;
    row-gap: var(--space-sm); /* 10px @16 */
  }

  .mp-actions__end {
    flex-wrap: wrap;
    justify-content: flex-end;
  }
}
</style>
