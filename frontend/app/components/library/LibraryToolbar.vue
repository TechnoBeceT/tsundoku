<script setup lang="ts">
import { computed } from 'vue'
import SearchInput from '../ui/SearchInput.vue'
import SelectField from '../ui/SelectField.vue'
import IconButton from '../ui/IconButton.vue'
import type { SelectOption } from '../ui/forms.types'
import type { SortKey, SortDir } from './librarySort'
import { defaultDirFor } from './librarySort'
import type { LibraryFilters } from './libraryFilter'

/**
 * LibraryToolbar — the library grid's search + sort + filter bar.
 *
 * A `SearchInput` (title search), a `SelectField` picking the sort FIELD, an
 * asc/desc direction toggle beside it (a chevron `IconButton` — flips the
 * direction of whatever field is active), and a row of toggle-chips
 * (Downloaded / Unread / Completed / Needs source). The Komikku/Suwayomi
 * parity model: field and direction are independent controls, and the filters
 * stack (logical AND) on top of the category tab.
 *
 * Presentation only: props down, events up — no fetch, no store, no useLibrary.
 * It composes shared `ui/` atoms and references only design tokens, so it
 * renders correctly in both themes. The filter toggles are small local pill
 * buttons (not a new shared atom) styled off the same tokens as the app's other
 * filter chips.
 */
const props = defineProps<{
  /** The current search string (v-model:search). */
  search: string
  /** The active sort field. */
  sortKey: SortKey
  /** The active sort direction. */
  sortDir: SortDir
  /** The active toggle-filters (v-model:filters). */
  filters: LibraryFilters
}>()

const emit = defineEmits<{
  /** The search string changed ('' on clear). */
  'update:search': [value: string]
  /** The sort selection changed — carries the resolved key + direction. */
  'update:sort': [payload: { key: SortKey; dir: SortDir }]
  /** A filter toggle flipped — carries the NEW full filter set. */
  'update:filters': [value: LibraryFilters]
}>()

/**
 * The sort FIELD menu — one option per SortKey. Names follow Komikku's
 * LibrarySortMode as closely as the present data allows (Last-read,
 * Tracker-score and Chapter-fetch-date are deferred — they need data not yet in
 * the DTO). Kept as ONE readonly table so the label and the key can never drift.
 */
const SORT_OPTIONS = [
  { value: 'title', label: 'Alphabetical' },
  { value: 'added', label: 'Date added' },
  { value: 'updated', label: 'Latest chapter' },
  { value: 'total', label: 'Total chapters' },
  { value: 'unread', label: 'Unread count' },
  { value: 'random', label: 'Random' },
] as const satisfies readonly { value: SortKey; label: string }[]

const selectOptions: SelectOption[] = SORT_OPTIONS.map((o) => ({ value: o.value, label: o.label }))

/**
 * The filter chips — one per LibraryFilters key, with its label + icon. ONE
 * table so the label, the flipped key, and the icon stay in lockstep.
 */
const FILTER_CHIPS = [
  { key: 'downloaded', label: 'Downloaded', icon: 'lucide:circle-check-big' },
  { key: 'unread', label: 'Unread', icon: 'lucide:book-open' },
  { key: 'completed', label: 'Completed', icon: 'lucide:flag' },
  { key: 'needsSource', label: 'Needs source', icon: 'lucide:triangle-alert' },
] as const satisfies readonly { key: keyof LibraryFilters; label: string; icon: string }[]

// Selecting a field applies that field's canonical default direction; the
// explicit toggle beside it then overrides. (Re-picking the current field
// re-applies its default — a cheap, predictable "reset direction".)
function onSortField(value: string): void {
  const key = value as SortKey
  emit('update:sort', { key, dir: defaultDirFor(key) })
}

// The direction toggle flips asc↔desc for the CURRENT field.
function toggleDir(): void {
  emit('update:sort', { key: props.sortKey, dir: props.sortDir === 'asc' ? 'desc' : 'asc' })
}

const dirLabel = computed(() => (props.sortDir === 'asc' ? 'Ascending' : 'Descending'))
const dirIcon = computed(() => (props.sortDir === 'asc' ? 'lucide:arrow-up-narrow-wide' : 'lucide:arrow-down-wide-narrow'))

function toggleFilter(key: keyof LibraryFilters): void {
  emit('update:filters', { ...props.filters, [key]: !props.filters[key] })
}
</script>

<template>
  <div class="toolbar">
    <div class="toolbar__top">
      <div class="toolbar__search">
        <SearchInput
          :model-value="search"
          placeholder="Search your library…"
          @update:model-value="emit('update:search', $event)"
        />
      </div>
      <div class="toolbar__sort">
        <SelectField
          class="toolbar__sort-field"
          :model-value="sortKey"
          :options="selectOptions"
          aria-label="Sort series by"
          @update:model-value="onSortField"
        />
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
        <IconButton class="toolbar__dir" size="md" :ariaLabel="`Sort direction: ${dirLabel} (toggle)`" @click="toggleDir">
          <Icon :name="dirIcon" />
        </IconButton>
      </div>
    </div>
    <div class="toolbar__filters">
      <button
        v-for="chip in FILTER_CHIPS"
        :key="chip.key"
        type="button"
        class="toolbar__chip"
        :class="{ 'toolbar__chip--on': filters[chip.key] }"
        :aria-pressed="filters[chip.key]"
        @click="toggleFilter(chip.key)"
      >
        <Icon :name="chip.icon" />
        {{ chip.label }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.toolbar__top {
  display: flex;
  align-items: center;
  gap: 12px;
}

/* The search grows to fill the row; the sort controls keep their intrinsic width. */
.toolbar__search {
  flex: 1 1 auto;
  min-width: 0;
}

.toolbar__sort {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
}

.toolbar__sort-field {
  flex: 0 0 auto;
}

/* The direction toggle rides the same 38px control height as the field/search
 * beside it, so the row aligns (IconButton's default square is smaller). */
.toolbar__dir {
  width: 38px;
  height: 38px;
}

/* The filter chip row — a wrapping strip of small pill toggles. */
.toolbar__filters {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

/* Filter toggle — a small pill button, off by default (neutral surface) and
 * accent-tinted when active, mirroring the app's other filter-chip treatments
 * (e.g. SourceFilterChips' imp-chip--on). Kept local here rather than a new
 * shared atom since these are single boolean toggles. */
.toolbar__chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex: 0 0 auto;
  padding: 0 12px;
  height: 32px;
  border-radius: var(--radius-pill);
  border: 1px solid var(--border);
  background: var(--surface2);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  white-space: nowrap;
  cursor: pointer;
  transition: all 0.15s;
}

.toolbar__chip:hover {
  border-color: var(--accent);
  color: var(--text);
}

.toolbar__chip--on {
  border-color: var(--accent);
  background: var(--accentSoft);
  color: var(--accentBright);
}

@media (max-width: 900px) {
  /* At app-breakpoint width the SelectField's intrinsic content width squeezes
   * the search box beside it; stack the search onto its own full-width row with
   * the sort field + direction toggle sharing the row below it. */
  .toolbar__top {
    flex-wrap: wrap;
  }

  .toolbar__search {
    flex: 1 1 100%;
  }

  .toolbar__sort {
    flex: 1 1 100%;
  }

  .toolbar__sort-field {
    flex: 1 1 auto;
  }

  .toolbar__chip {
    flex: 1 1 auto;
    justify-content: center;
  }
}
</style>
