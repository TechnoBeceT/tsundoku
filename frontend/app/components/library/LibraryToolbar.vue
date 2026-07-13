<script setup lang="ts">
import { computed } from 'vue'
import SearchInput from '../ui/SearchInput.vue'
import SelectField from '../ui/SelectField.vue'
import type { SelectOption } from '../ui/forms.types'
import type { SortKey, SortDir } from './librarySort'

/**
 * LibraryToolbar — the library grid's search + sort bar. A `SearchInput` (title
 * search) beside a `SelectField` whose five options each map to a `{key, dir}`
 * sort pair (title A–Z / Z–A, recently added / updated, most unread).
 *
 * Presentation only: props down, events up — no fetch, no store, no useLibrary.
 * It composes the shared `SearchInput`/`SelectField` atoms and references only
 * design tokens, so it renders correctly in both themes.
 */
const props = defineProps<{
  /** The current search string (v-model:search). */
  search: string
  /** The active sort field. */
  sortKey: SortKey
  /** The active sort direction. */
  sortDir: SortDir
}>()

const emit = defineEmits<{
  /** The search string changed ('' on clear). */
  'update:search': [value: string]
  /** The sort selection changed — carries the resolved key + direction. */
  'update:sort': [payload: { key: SortKey; dir: SortDir }]
}>()

/**
 * The sort menu: one flat `value` per option, translated to/from the `{key, dir}`
 * pair the parent understands. Kept as ONE readonly table so the label, the
 * select value, and the emitted key/dir can never drift apart.
 */
const SORT_OPTIONS = [
  { value: 'title-asc', label: 'Title A–Z', key: 'title', dir: 'asc' },
  { value: 'title-desc', label: 'Title Z–A', key: 'title', dir: 'desc' },
  { value: 'added-desc', label: 'Recently added', key: 'added', dir: 'desc' },
  { value: 'updated-desc', label: 'Recently updated', key: 'updated', dir: 'desc' },
  { value: 'unread-desc', label: 'Most unread', key: 'unread', dir: 'desc' },
] as const satisfies readonly { value: string; label: string; key: SortKey; dir: SortDir }[]

const selectOptions: SelectOption[] = SORT_OPTIONS.map((o) => ({ value: o.value, label: o.label }))

// The current select value — the flat key matching the active {key, dir} pair.
// Falls back to the first option if the pair has no listed combination.
const selected = computed(
  () => SORT_OPTIONS.find((o) => o.key === props.sortKey && o.dir === props.sortDir)?.value
    ?? SORT_OPTIONS[0].value,
)

function onSort(value: string): void {
  const opt = SORT_OPTIONS.find((o) => o.value === value)
  if (opt) emit('update:sort', { key: opt.key, dir: opt.dir })
}
</script>

<template>
  <div class="toolbar">
    <div class="toolbar__search">
      <SearchInput
        :model-value="search"
        placeholder="Search your library…"
        @update:model-value="emit('update:search', $event)"
      />
    </div>
    <SelectField
      class="toolbar__sort"
      :model-value="selected"
      :options="selectOptions"
      aria-label="Sort series"
      @update:model-value="onSort"
    />
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
}

/* The search grows to fill the row; the sort keeps its intrinsic width. */
.toolbar__search {
  flex: 1 1 auto;
  min-width: 0;
}

.toolbar__sort {
  flex: 0 0 auto;
}

@media (max-width: 900px) {
  /* At app-breakpoint width the SelectField's intrinsic content width (e.g.
   * "Recently updated") squeezes the search box down to nearly nothing
   * beside it. Stack instead: search takes the full row, sort drops to its
   * own full-width row below it — both stay comfortably tappable and neither
   * is ever cramped enough to overflow. */
  .toolbar {
    flex-wrap: wrap;
  }

  .toolbar__search {
    flex: 1 1 100%;
  }

  .toolbar__sort {
    flex: 1 1 100%;
  }
}
</style>
