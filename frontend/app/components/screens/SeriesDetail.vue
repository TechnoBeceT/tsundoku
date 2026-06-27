<script setup lang="ts">
import { computed, ref } from 'vue'
import BrandMark from '../ui/BrandMark.vue'
import type {
  Chapter,
  ChapterState,
  DeleteChoice,
  Provider,
  ProviderHealth,
  SeriesDetail,
} from './seriesDetail.types'

/**
 * SeriesDetail — the full single-series management screen: a cover/title header
 * with chapter-count stats + monitored/completed toggles + category select, the
 * (planned) metadata-source picker, a colour-coded chapter table, a ranked
 * source list (reorder / remove / add), plus the required-choice delete dialog
 * and the remove-source confirm dialog.
 *
 * Presentation only: ALL data arrives via props and every action is emitted —
 * the screen never fetches, routes, or mutates the backend. It honours §16 by
 * surfacing loading (busy spinners / disabled controls) and error (a dismissible
 * banner) states; success is reflected when the parent feeds back an updated
 * `series` prop. Token-only colours, so it reads correctly in both themes.
 */
const props = withDefaults(defineProps<{
  /** The series to render (summary fields + chapters + providers). */
  series: SeriesDetail
  /** Category names for the recategorize select (dynamic, user-defined list). */
  categoryOptions: string[]
  /** True while an inline mutation (toggle/category/reorder/metadata) is in flight. */
  saving?: boolean
  /** True while the delete request is in flight (dialog confirm spinner). */
  deleteBusy?: boolean
  /** True while the remove-source request is in flight (dialog confirm spinner). */
  removeBusy?: boolean
  /** A failed-mutation message to surface, or null/"" when there is none. */
  error?: string | null
}>(), {
  saving: false,
  deleteBusy: false,
  removeBusy: false,
  error: null,
})

const emit = defineEmits<{
  /** The category select changed — carries the new category name. */
  changeCategory: [category: string]
  /** The monitored toggle flipped — carries the NEW value. */
  toggleMonitored: [monitored: boolean]
  /** The completed toggle flipped — carries the NEW value. */
  toggleCompleted: [completed: boolean]
  /** Providers were re-ranked — carries the full updated {id, importance} list. */
  reorderProviders: [providers: { id: string, importance: number }[]]
  /** A source removal was confirmed — carries the SeriesProvider id. */
  removeSource: [providerId: string]
  /** A metadata source was picked — carries the SeriesProvider id. */
  chooseMetadataSource: [providerId: string]
  /** The series delete was confirmed — carries the required deleteFiles choice. */
  deleteSeries: [deleteFiles: boolean]
  /** The owner asked to add a source (→ the import flow). */
  addSource: []
  /** The error banner was dismissed. */
  dismissError: []
}>()

// ---- Display maps (label + token key per state / health) -------------------
// Mapping a state/health to its CSS-token KEY (not a colour) keeps every badge
// hue in tokens/seriesDetail.css — the badge element only references var(--…).
const STATE_LABELS: Record<ChapterState, string> = {
  wanted: 'Wanted',
  downloading: 'Downloading',
  downloaded: 'On disk',
  upgrade_available: 'Upgrade ready',
  upgrading: 'Upgrading',
  failed: 'Failed',
  permanently_failed: 'Failed · final',
}
const STATE_KEYS: Record<ChapterState, string> = {
  wanted: 'wanted',
  downloading: 'downloading',
  downloaded: 'downloaded',
  upgrade_available: 'upgrade',
  upgrading: 'upgrading',
  failed: 'failed',
  permanently_failed: 'permfailed',
}
const HEALTH_LABELS: Record<ProviderHealth, string> = {
  ok: 'Healthy',
  stale: 'Stale',
  erroring: 'Erroring',
}

// ---- Derived data ----------------------------------------------------------
// Chapters ordered by number (null sorts as 0) then by stable key — matches the
// backend's "ordered by number then chapterKey" contract.
const sortedChapters = computed<Chapter[]>(() =>
  [...props.series.chapters].sort(
    (a, b) => (a.number ?? 0) - (b.number ?? 0) || a.chapterKey.localeCompare(b.chapterKey),
  ),
)

// Sources ordered by importance descending — the top one is "Preferred".
const sortedProviders = computed<Provider[]>(() =>
  [...props.series.providers].sort((a, b) => b.importance - a.importance),
)

const counts = computed(() => props.series.chapterCounts)

// The currently-pinned metadata source (or the preferred one when auto/unset).
const metaActiveId = computed(
  () => props.series.metadataProviderId ?? sortedProviders.value[0]?.id ?? null,
)

// ---- Badge style helpers ---------------------------------------------------
// Returns local custom-prop overrides pointing at the per-state/health tokens,
// so the badge markup stays generic (one .badge rule reads --badge-*).
const stateBadgeVars = (s: ChapterState): Record<string, string> => ({
  '--badge-fg': `var(--sd-st-${STATE_KEYS[s]}-fg)`,
  '--badge-bg': `var(--sd-st-${STATE_KEYS[s]}-bg)`,
  '--badge-dot': `var(--sd-st-${STATE_KEYS[s]}-dot)`,
})
const healthBadgeVars = (h: ProviderHealth): Record<string, string> => ({
  '--badge-fg': `var(--sd-hl-${h}-fg)`,
  '--badge-bg': `var(--sd-hl-${h}-bg)`,
  '--badge-dot': `var(--sd-hl-${h}-dot)`,
})

// ---- Chapter row formatting ------------------------------------------------
// Display name: provider title, else "Chapter N", else an em-dash placeholder.
const chapterName = (c: Chapter): string =>
  c.name || (c.number != null ? `Chapter ${c.number}` : '—')
const chapterNumber = (c: Chapter): string => (c.number == null ? '—' : String(c.number))
const pageLabel = (c: Chapter): string => (c.pageCount == null ? '' : `${c.pageCount}p`)

// Relative-time label for sync/newest timestamps (null → "never").
const rel = (iso: string | null): string => {
  if (iso == null) return 'never'
  const d = Date.now() - Date.parse(iso)
  const m = 60_000, h = 3_600_000, day = 86_400_000
  if (d < m) return 'just now'
  if (d < h) return `${Math.floor(d / m)}m ago`
  if (d < day) return `${Math.floor(d / h)}h ago`
  return `${Math.floor(d / day)}d ago`
}

// The tag under each metadata-source card: the preferred source is the default.
const metaTag = (p: Provider): string =>
  p.id === sortedProviders.value[0]?.id ? 'Preferred · default' : `importance ${p.importance}`

// ---- Reorder ---------------------------------------------------------------
// Move a source up (dir -1) or down (dir +1) one rank, then emit the FULL list
// with the existing importance values reassigned by new position (higher rank =
// higher importance) — the API applies the batch all-or-nothing.
const moveProvider = (id: string, dir: -1 | 1): void => {
  if (props.saving) return
  const list = [...sortedProviders.value]
  const i = list.findIndex((p) => p.id === id)
  const j = i + dir
  if (j < 0 || j >= list.length) return
  ;[list[i], list[j]] = [list[j]!, list[i]!]
  const importances = sortedProviders.value.map((p) => p.importance).sort((a, b) => b - a)
  emit('reorderProviders', list.map((p, idx) => ({ id: p.id, importance: importances[idx]! })))
}

// ---- Toggles + category ----------------------------------------------------
const onToggleMonitored = (): void => {
  if (!props.saving) emit('toggleMonitored', !props.series.monitored)
}
const onToggleCompleted = (): void => {
  if (!props.saving) emit('toggleCompleted', !props.series.completed)
}
const onChangeCategory = (e: Event): void => {
  emit('changeCategory', (e.target as HTMLSelectElement).value)
}
const onPickMeta = (id: string): void => {
  if (!props.saving) emit('chooseMetadataSource', id)
}

// ---- Delete dialog (required choice) ---------------------------------------
const deleteOpen = ref(false)
const deleteChoice = ref<DeleteChoice>('keep')
const openDelete = (): void => {
  deleteChoice.value = 'keep'
  deleteOpen.value = true
}
const closeDelete = (): void => {
  if (!props.deleteBusy) deleteOpen.value = false
}
const confirmDelete = (): void => emit('deleteSeries', deleteChoice.value === 'wipe')
const deleteBtnLabel = computed(() => (deleteChoice.value === 'wipe' ? 'Delete + files' : 'Un-manage'))

// ---- Remove-source dialog --------------------------------------------------
const removeOpen = ref(false)
const removeTargetId = ref<string | null>(null)
const openRemove = (id: string): void => {
  removeTargetId.value = id
  removeOpen.value = true
}
const closeRemove = (): void => {
  if (!props.removeBusy) removeOpen.value = false
}
const confirmRemove = (): void => {
  if (removeTargetId.value) emit('removeSource', removeTargetId.value)
}
const removeName = computed(
  () => props.series.providers.find((p) => p.id === removeTargetId.value)?.provider ?? '',
)
</script>

<template>
  <div class="detail">
    <!-- §16 error banner: a failed mutation surfaces here, dismissible -->
    <div v-if="error" class="detail__error" role="alert">
      <span class="detail__error-text">{{ error }}</span>
      <button type="button" class="detail__error-close" aria-label="Dismiss" @click="emit('dismissError')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true">
          <path d="M18 6L6 18M6 6l12 12" />
        </svg>
      </button>
    </div>

    <!-- ===== Header card ===================================================== -->
    <section class="header">
      <div class="header__cover">
        <img v-if="series.coverUrl" class="header__img" :src="series.coverUrl" :alt="`${series.title} cover`">
        <div v-else class="header__placeholder">
          <BrandMark :size="64" tone="inverse" />
        </div>
      </div>

      <div class="header__main">
        <div class="header__titlerow">
          <div class="header__titlebox">
            <span class="chip chip--category">{{ series.category }}</span>
            <h1 class="header__title">{{ series.title }}</h1>
          </div>
          <button type="button" class="btn-delete" @click="openDelete">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
            </svg>
            Delete
          </button>
        </div>

        <!-- Chapter-count stats -->
        <div class="stats">
          <div class="stat">
            <div class="stat__label">Total</div>
            <div class="stat__value">{{ counts.total }}</div>
          </div>
          <div class="stat">
            <div class="stat__label">On disk</div>
            <div class="stat__value stat__value--disk">{{ counts.downloaded }}</div>
          </div>
          <div class="stat">
            <div class="stat__label">Wanted</div>
            <div class="stat__value">{{ counts.wanted }}</div>
          </div>
          <div class="stat">
            <div class="stat__label">Failed</div>
            <div class="stat__value stat__value--failed">{{ counts.failed }}</div>
          </div>
        </div>

        <!-- Toggles + category select -->
        <div class="controls">
          <div class="control">
            <button
              type="button"
              class="switch"
              :class="{ 'switch--on': series.monitored }"
              role="switch"
              :aria-checked="series.monitored"
              aria-label="Monitored"
              :disabled="saving"
              @click="onToggleMonitored"
            >
              <span class="switch__knob" />
            </button>
            <div>
              <div class="control__title">Monitored</div>
              <div class="control__hint">Auto-check for new chapters</div>
            </div>
          </div>

          <div class="control">
            <button
              type="button"
              class="switch"
              :class="{ 'switch--on': series.completed }"
              role="switch"
              :aria-checked="series.completed"
              aria-label="Completed"
              :disabled="saving"
              @click="onToggleCompleted"
            >
              <span class="switch__knob" />
            </button>
            <div>
              <div class="control__title">Completed</div>
              <div class="control__hint">Mark finished · skip sweeps</div>
            </div>
          </div>

          <label class="control control--category">
            <span class="control__catlabel">Category</span>
            <select class="select" :value="series.category" :disabled="saving" @change="onChangeCategory">
              <option v-for="c in categoryOptions" :key="c" :value="c">{{ c }}</option>
            </select>
          </label>
        </div>
      </div>
    </section>

    <!-- ===== Metadata source (planned) ===================================== -->
    <section class="panel meta">
      <div class="meta__head">
        <span class="panel__title">Metadata source</span>
        <span class="chip chip--planned">PLANNED</span>
      </div>
      <p class="meta__desc">
        Which source supplies the title &amp; cover shown across the app. Defaults to the preferred source.
      </p>
      <div class="meta__cards">
        <button
          v-for="p in sortedProviders"
          :key="p.id"
          type="button"
          class="metacard"
          :class="{ 'metacard--active': p.id === metaActiveId }"
          :disabled="saving"
          @click="onPickMeta(p.id)"
        >
          <span class="metacard__cover">
            <BrandMark :size="22" tone="inverse" />
          </span>
          <span class="metacard__body">
            <span class="metacard__name">
              {{ p.provider }}
              <svg v-if="p.id === metaActiveId" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" class="metacard__check" aria-hidden="true">
                <path d="M20 6L9 17l-5-5" />
              </svg>
            </span>
            <span class="metacard__title">{{ series.title }}</span>
            <span class="metacard__tag">{{ metaTag(p) }}</span>
          </span>
        </button>
      </div>
    </section>

    <!-- ===== Chapters + Sources ============================================ -->
    <div class="columns">
      <!-- Chapters -->
      <section class="panel chapters">
        <div class="panel__head">
          <span class="panel__title">Chapters</span>
          <span class="count-pill">{{ counts.total }}</span>
        </div>
        <div class="chapters__scroll">
          <div v-for="ch in sortedChapters" :key="ch.chapterKey" class="chapter">
            <div class="chapter__num">{{ chapterNumber(ch) }}</div>
            <div class="chapter__main">
              <div class="chapter__name">{{ chapterName(ch) }}</div>
              <div v-if="ch.filename" class="chapter__file">{{ ch.filename }}</div>
            </div>
            <span v-if="pageLabel(ch)" class="chapter__pages">{{ pageLabel(ch) }}</span>
            <span class="badge" :style="stateBadgeVars(ch.state)">
              <span class="badge__dot" />{{ STATE_LABELS[ch.state] }}
            </span>
          </div>
        </div>
      </section>

      <!-- Sources -->
      <section class="panel sources">
        <div class="panel__head">
          <div class="panel__headleft">
            <span class="panel__title">Sources</span>
            <span class="count-pill">{{ series.providers.length }}</span>
          </div>
          <button type="button" class="btn-add" @click="emit('addSource')">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true">
              <path d="M12 5v14M5 12h14" />
            </svg>
            Add
          </button>
        </div>

        <div class="sources__body">
          <div v-if="sortedProviders.length > 0" class="sources__eyebrow">Preferred first</div>

          <div v-for="(p, idx) in sortedProviders" :key="p.id" class="source">
            <div class="source__rank">
              <button
                type="button"
                class="arrow"
                aria-label="Increase priority"
                :disabled="idx === 0 || saving"
                @click="moveProvider(p.id, -1)"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M18 15l-6-6-6 6" />
                </svg>
              </button>
              <span class="rank" :class="{ 'rank--top': idx === 0 }">{{ idx + 1 }}</span>
              <button
                type="button"
                class="arrow"
                aria-label="Decrease priority"
                :disabled="idx === sortedProviders.length - 1 || saving"
                @click="moveProvider(p.id, 1)"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M6 9l6 6 6-6" />
                </svg>
              </button>
            </div>

            <div class="source__main">
              <div class="source__namerow">
                <span class="source__name">{{ p.provider }}</span>
                <span v-if="idx === 0" class="chip chip--preferred">PREFERRED</span>
              </div>
              <div class="source__meta">
                <span class="lang">{{ p.language.toUpperCase() }}</span>
                <span v-if="p.scanlator">{{ p.scanlator }}</span>
                <span>importance {{ p.importance }}</span>
              </div>
              <div class="source__healthrow">
                <span class="badge" :style="healthBadgeVars(p.health)">
                  <span class="badge__dot" />{{ HEALTH_LABELS[p.health] }}
                </span>
                <span v-if="p.chaptersBehind > 0" class="source__behind">{{ p.chaptersBehind }} behind</span>
              </div>
              <div class="source__times">
                <span>Synced {{ rel(p.lastSyncedAt) }}</span>
                <span>Newest {{ rel(p.newestChapterAt) }}</span>
              </div>
              <div v-if="p.lastError" class="source__error">{{ p.lastError }}</div>
              <div class="source__actions">
                <button type="button" class="btn-remove" :disabled="saving" @click="openRemove(p.id)">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
                  </svg>
                  Remove
                </button>
              </div>
            </div>
          </div>

          <div v-if="sortedProviders.length === 0" class="sources__empty">
            No sources tracked. The series stays in your library.
          </div>
        </div>
      </section>
    </div>

    <!-- ===== Delete dialog (required choice) =============================== -->
    <div v-if="deleteOpen" class="overlay" @click.self="closeDelete">
      <div class="dialog" role="dialog" aria-modal="true" aria-label="Delete series">
        <div class="dialog__title">Delete “{{ series.title }}”?</div>
        <div class="dialog__desc">Choose what happens to downloaded files. You must pick one.</div>

        <button
          type="button"
          class="radiocard"
          :class="{ 'radiocard--active': deleteChoice === 'keep' }"
          @click="deleteChoice = 'keep'"
        >
          <span class="radiodot" :class="{ 'radiodot--active': deleteChoice === 'keep' }" />
          <span class="radiocard__body">
            <span class="radiocard__title">Keep files on disk</span>
            <span class="radiocard__hint">Removes library tracking only. Recoverable later via a library rescan.</span>
          </span>
        </button>

        <button
          type="button"
          class="radiocard radiocard--danger"
          :class="{ 'radiocard--active-danger': deleteChoice === 'wipe' }"
          @click="deleteChoice = 'wipe'"
        >
          <span class="radiodot radiodot--danger" :class="{ 'radiodot--active-danger': deleteChoice === 'wipe' }" />
          <span class="radiocard__body">
            <span class="radiocard__title radiocard__title--danger">Also delete downloaded files</span>
            <span class="radiocard__hint">Permanently removes all CBZ files from disk. This cannot be undone.</span>
          </span>
        </button>

        <div class="dialog__actions">
          <button type="button" class="btn-cancel" :disabled="deleteBusy" @click="closeDelete">Cancel</button>
          <button
            type="button"
            class="btn-confirm"
            :class="{ 'btn-confirm--danger': deleteChoice === 'wipe' }"
            :disabled="deleteBusy"
            @click="confirmDelete"
          >
            <span v-if="deleteBusy" class="spinner" />
            {{ deleteBtnLabel }}
          </button>
        </div>
      </div>
    </div>

    <!-- ===== Remove-source dialog ========================================= -->
    <div v-if="removeOpen" class="overlay" @click.self="closeRemove">
      <div class="dialog dialog--narrow" role="dialog" aria-modal="true" aria-label="Remove source">
        <div class="dialog__title">Remove “{{ removeName }}”?</div>
        <div class="dialog__desc">
          This removes the source feed only. All downloaded CBZ files and chapters are kept. You can re-add it later.
        </div>
        <div class="dialog__actions">
          <button type="button" class="btn-cancel" :disabled="removeBusy" @click="closeRemove">Cancel</button>
          <button type="button" class="btn-confirm btn-confirm--danger" :disabled="removeBusy" @click="confirmRemove">
            <span v-if="removeBusy" class="spinner" />
            Remove source
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.detail {
  padding: 24px 30px 70px;
  background: var(--bg);
  min-height: 100%;
}

/* ---- Error banner --------------------------------------------------------- */
.detail__error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
  padding: 11px 14px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-text);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
}

.detail__error-close {
  display: flex;
  flex: none;
  padding: 4px;
  border: none;
  border-radius: var(--radius-xs);
  background: transparent;
  color: var(--danger-text);
  cursor: pointer;
}

.detail__error-close:hover {
  background: var(--danger-bg-hover);
}

/* ---- Shared chips --------------------------------------------------------- */
.chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
}

.chip--category {
  background: var(--accentSoft);
  color: var(--accentBright);
}

.chip--planned {
  padding: 2px 7px;
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.04em;
  background: var(--surface3);
  color: var(--faint);
}

.chip--preferred {
  padding: 2px 7px;
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.04em;
  background: var(--accentSoft);
  color: var(--accentBright);
}

/* ---- Header --------------------------------------------------------------- */
.header {
  display: flex;
  gap: 22px;
  flex-wrap: wrap;
  margin-bottom: 18px;
  padding: 20px;
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
}

.header__cover {
  position: relative;
  width: 152px;
  flex: none;
  border-radius: 13px;
  overflow: hidden;
  border: 1px solid var(--border);
}

.header__cover::before {
  content: '';
  display: block;
  padding-bottom: 140%;
}

.header__img,
.header__placeholder {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
}

.header__img {
  object-fit: cover;
}

.header__placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--cover-placeholder);
}

.header__main {
  flex: 1;
  min-width: 260px;
  display: flex;
  flex-direction: column;
  gap: 15px;
}

.header__titlerow {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.header__titlebox {
  flex: 1;
  min-width: 0;
}

.header__title {
  margin: 9px 0 0;
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: var(--text-3xl);
  line-height: 1.12;
  color: var(--text);
}

.btn-delete {
  display: flex;
  align-items: center;
  gap: 7px;
  flex: none;
  padding: 8px 13px;
  border-radius: var(--radius-md);
  border: 1px solid var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-bright);
  font-size: 12.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: background 0.15s;
}

.btn-delete:hover {
  background: var(--danger-bg-hover);
}

/* ---- Stats ---------------------------------------------------------------- */
.stats {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

.stat {
  flex: 1;
  min-width: 88px;
  padding: 11px 13px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--surface2);
}

.stat__label {
  margin-bottom: 3px;
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--faint);
}

.stat__value {
  font-family: var(--font-display);
  font-size: var(--text-2xl);
  font-weight: var(--weight-extrabold);
  color: var(--text);
}

.stat__value--disk {
  color: var(--sd-stat-disk);
}

.stat__value--failed {
  color: var(--danger-bright);
}

/* ---- Controls (toggles + category) ---------------------------------------- */
.controls {
  display: flex;
  align-items: center;
  gap: 24px;
  flex-wrap: wrap;
  margin-top: 2px;
}

.control {
  display: flex;
  align-items: center;
  gap: 11px;
}

.control--category {
  margin-left: auto;
  gap: 9px;
  cursor: pointer;
}

.control__title {
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.control__hint {
  font-size: var(--text-xs);
  color: var(--faint);
}

.control__catlabel {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--faint);
}

.switch {
  position: relative;
  width: 44px;
  height: 25px;
  flex: none;
  padding: 0;
  border-radius: var(--radius-pill);
  border: 1px solid var(--border);
  background: var(--surface3);
  cursor: pointer;
  transition: background 0.2s;
}

.switch--on {
  background: var(--accent);
}

.switch:disabled {
  cursor: default;
  opacity: 0.6;
}

.switch__knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 19px;
  height: 19px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.4);
  transition: left 0.2s;
}

.switch--on .switch__knob {
  left: 21px;
}

.select {
  padding: 8px 12px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  cursor: pointer;
  outline: none;
}

.select:disabled {
  cursor: default;
  opacity: 0.6;
}

/* ---- Panels (shared card chrome) ------------------------------------------ */
.panel {
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
  overflow: hidden;
  min-width: 0;
}

.panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 9px;
  padding: 15px 18px;
  border-bottom: 1px solid var(--border);
}

.panel__headleft {
  display: flex;
  align-items: center;
  gap: 9px;
}

.panel__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: 15px;
  color: var(--text);
}

.count-pill {
  padding: 1px 8px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}

/* ---- Metadata source ------------------------------------------------------ */
.meta {
  margin-bottom: 18px;
  padding: 16px 18px;
}

.meta__head {
  display: flex;
  align-items: center;
  gap: 9px;
  margin-bottom: 4px;
}

.meta__desc {
  margin: 0 0 13px;
  font-size: var(--text-sm);
  color: var(--faint);
}

.meta__cards {
  display: flex;
  gap: 11px;
  flex-wrap: wrap;
}

.metacard {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 210px;
  padding: 9px 11px;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--border);
  background: var(--surface2);
  cursor: pointer;
  text-align: left;
  transition: all 0.15s;
}

.metacard--active {
  border-color: var(--accent);
  background: var(--accentSoft);
}

.metacard:disabled {
  cursor: default;
  opacity: 0.7;
}

.metacard__cover {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 54px;
  flex: none;
  border-radius: 7px;
  background: var(--cover-placeholder);
}

.metacard__body {
  min-width: 0;
}

.metacard__name {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 2px;
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.metacard__check {
  color: var(--accentBright);
  flex: none;
}

.metacard__title {
  display: block;
  max-width: 160px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: var(--text-xs);
  color: var(--muted);
}

.metacard__tag {
  display: block;
  margin-top: 2px;
  font-size: 10px;
  color: var(--faint);
}

/* ---- Two-column layout ---------------------------------------------------- */
.columns {
  display: grid;
  grid-template-columns: 1.55fr 1fr;
  gap: 18px;
  align-items: start;
}

/* ---- Chapter list --------------------------------------------------------- */
.chapters__scroll {
  max-height: 580px;
  overflow: auto;
}

.chapter {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 11px 18px;
  border-bottom: 1px solid var(--border);
}

.chapter__num {
  width: 40px;
  flex: none;
  font-family: var(--font-mono);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

.chapter__main {
  flex: 1;
  min-width: 0;
}

.chapter__name {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 13.5px;
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.chapter__file {
  margin-top: 2px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-family: var(--font-mono);
  font-size: 10.5px;
  color: var(--faint);
}

.chapter__pages {
  flex: none;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--faint);
}

/* ---- State / health badge (token-driven) ---------------------------------- */
.badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  flex: none;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
  color: var(--badge-fg);
  background: var(--badge-bg);
}

.badge__dot {
  width: 6px;
  height: 6px;
  flex-shrink: 0;
  border-radius: var(--radius-pill);
  background: var(--badge-dot);
}

/* ---- Sources -------------------------------------------------------------- */
.btn-add {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 6px 11px;
  border-radius: 9px;
  border: 1px solid var(--border2);
  background: var(--surface2);
  color: var(--text);
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.btn-add:hover {
  border-color: var(--accent);
  color: var(--accentBright);
}

.sources__body {
  padding: 12px;
}

.sources__eyebrow {
  margin: 2px 4px 9px;
  font-size: 10.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--faint);
}

.source {
  display: flex;
  align-items: flex-start;
  gap: 11px;
  margin-bottom: 10px;
  padding: 12px;
  border-radius: 13px;
  border: 1px solid var(--border);
  background: var(--surface2);
}

.source__rank {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 5px;
  flex: none;
}

.arrow {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 18px;
  padding: 0;
  border-radius: var(--radius-xs);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  cursor: pointer;
}

.arrow:disabled {
  color: var(--faint);
  opacity: 0.4;
  cursor: default;
}

.rank {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: var(--radius-sm);
  background: var(--surface3);
  color: var(--muted);
  font-size: var(--text-sm);
  font-weight: var(--weight-extrabold);
}

.rank--top {
  background: var(--accent);
  color: #fff;
}

.source__main {
  flex: 1;
  min-width: 0;
}

.source__namerow {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 5px;
  flex-wrap: wrap;
}

.source__name {
  font-size: var(--text-md);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.source__meta {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-bottom: 8px;
  flex-wrap: wrap;
  font-size: 11.5px;
  color: var(--muted);
}

.lang {
  padding: 1px 6px;
  border-radius: 5px;
  background: var(--surface3);
  color: var(--muted);
  font-size: 10px;
  font-weight: var(--weight-bold);
}

.source__healthrow {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.source__behind {
  font-size: var(--text-xs);
  color: var(--faint);
}

.source__times {
  display: flex;
  gap: 14px;
  flex-wrap: wrap;
  margin-top: 8px;
  font-size: 10.5px;
  color: var(--faint);
}

.source__error {
  margin-top: 8px;
  padding: 6px 9px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-text);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  word-break: break-word;
}

.source__actions {
  margin-top: 9px;
}

.btn-remove {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 5px 10px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: transparent;
  color: var(--danger-bright);
  font-size: 11.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: background 0.15s;
}

.btn-remove:hover {
  background: var(--danger-bg);
}

.btn-remove:disabled {
  opacity: 0.5;
  cursor: default;
}

.sources__empty {
  padding: 24px 12px;
  text-align: center;
  font-size: 12.5px;
  color: var(--faint);
}

/* ---- Dialogs -------------------------------------------------------------- */
.overlay {
  position: fixed;
  inset: 0;
  z-index: 60;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(5, 4, 9, 0.66);
  backdrop-filter: blur(3px);
}

.dialog {
  width: 100%;
  max-width: 480px;
  padding: 24px;
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border2);
  background: var(--surface);
  box-shadow: var(--shadow);
}

.dialog--narrow {
  max-width: 430px;
}

.dialog__title {
  margin-bottom: 6px;
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-xl);
  color: var(--text);
}

.dialog__desc {
  margin-bottom: 18px;
  font-size: var(--text-base);
  line-height: 1.5;
  color: var(--muted);
}

.radiocard {
  display: flex;
  gap: 12px;
  width: 100%;
  margin-bottom: 10px;
  padding: 14px;
  border-radius: 13px;
  border: 1.5px solid var(--border);
  background: var(--surface2);
  text-align: left;
  cursor: pointer;
  transition: all 0.15s;
}

.radiocard--active {
  border-color: var(--accent);
  background: var(--accentSoft);
}

.radiocard--active-danger {
  border-color: var(--danger);
  background: var(--danger-bg);
}

.radiodot {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  margin-top: 1px;
  flex: none;
  border-radius: 50%;
  border: 2px solid var(--border2);
}

.radiodot--active {
  border-color: var(--accent);
}

.radiodot--active::after {
  content: '';
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--accent);
}

.radiodot--active-danger {
  border-color: var(--danger);
}

.radiodot--active-danger::after {
  content: '';
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--danger);
}

.radiocard__body {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.radiocard__title {
  font-size: var(--text-md);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.radiocard__title--danger {
  color: var(--danger-bright);
}

.radiocard__hint {
  font-size: var(--text-sm);
  line-height: 1.45;
  color: var(--muted);
}

.dialog__actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 22px;
}

.btn-cancel {
  padding: 10px 16px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: transparent;
  color: var(--text);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
}

.btn-cancel:disabled {
  opacity: 0.6;
  cursor: default;
}

.btn-confirm {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 18px;
  border-radius: var(--radius-md);
  border: none;
  background: var(--accent);
  color: #fff;
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
}

.btn-confirm--danger {
  background: var(--danger);
}

.btn-confirm:disabled {
  opacity: 0.7;
  cursor: default;
}

.spinner {
  width: 14px;
  height: 14px;
  border: 2px solid #fff;
  border-right-color: transparent;
  border-radius: 50%;
  display: inline-block;
  animation: detail-spin 0.8s linear infinite;
}

@keyframes detail-spin {
  to {
    transform: rotate(360deg);
  }
}

/* ---- Responsive ----------------------------------------------------------- */
@media (max-width: 900px) {
  .columns {
    grid-template-columns: 1fr;
  }
}
</style>
