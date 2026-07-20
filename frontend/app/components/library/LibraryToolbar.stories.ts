import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import LibraryToolbar from './LibraryToolbar.vue'
import type { SortKey, SortDir } from './librarySort'
import { NO_FILTERS, type LibraryFilters } from './libraryFilter'

/**
 * Stories for the library search + sort + filter toolbar. `Default` is
 * interactive (the search field, the sort field dropdown, the direction toggle,
 * and the filter chips all drive local state); the remaining stories pin each
 * sort field's selected state and the filter-active look. Flip the Storybook
 * theme toolbar to confirm the controls read in BOTH dark and light.
 */
const meta = {
  title: 'Library/LibraryToolbar',
  component: LibraryToolbar,
  // search/sortKey/sortDir/filters are required props; the interactive Default
  // overrides them with local refs, so these defaults only satisfy the CSF3
  // story typing.
  args: { search: '', sortKey: 'title', sortDir: 'asc', filters: { ...NO_FILTERS } },
} satisfies Meta<typeof LibraryToolbar>

export default meta
type Story = StoryObj<typeof meta>

/** Interactive — typing filters, the field dropdown + direction toggle re-sort,
 * and the filter chips toggle, all against local state. */
export const Default: Story = {
  render: () => ({
    components: { LibraryToolbar },
    setup() {
      const search = ref('')
      const sortKey = ref<SortKey>('title')
      const sortDir = ref<SortDir>('asc')
      const filters = ref<LibraryFilters>({ ...NO_FILTERS })
      const onSort = (p: { key: SortKey; dir: SortDir }): void => {
        sortKey.value = p.key
        sortDir.value = p.dir
      }
      const onFilters = (f: LibraryFilters): void => {
        filters.value = f
      }
      return { search, sortKey, sortDir, filters, onSort, onFilters }
    },
    template: `
      <div style="max-width:720px">
        <LibraryToolbar
          v-model:search="search"
          :sort-key="sortKey"
          :sort-dir="sortDir"
          :filters="filters"
          @update:sort="onSort"
          @update:filters="onFilters"
        />
      </div>
    `,
  }),
}

/** A search in progress — the clear × shows and the sort is Alphabetical. */
export const Searching: Story = {
  args: { search: 'solo leveling', sortKey: 'title', sortDir: 'asc' },
}

/** Alphabetical, descending (Z–A). */
export const TitleDescending: Story = {
  args: { search: '', sortKey: 'title', sortDir: 'desc' },
}

/** Date added selected. */
export const DateAdded: Story = {
  args: { search: '', sortKey: 'added', sortDir: 'desc' },
}

/** Latest chapter selected. */
export const LatestChapter: Story = {
  args: { search: '', sortKey: 'updated', sortDir: 'desc' },
}

/** Total chapters selected. */
export const TotalChapters: Story = {
  args: { search: '', sortKey: 'total', sortDir: 'desc' },
}

/** Unread count selected. */
export const UnreadCount: Story = {
  args: { search: '', sortKey: 'unread', sortDir: 'desc' },
}

/** Random selected. */
export const Random: Story = {
  args: { search: '', sortKey: 'random', sortDir: 'asc' },
}

/** Several toggle-filters active — the chips read accent/pressed. */
export const FiltersActive: Story = {
  args: {
    search: '',
    sortKey: 'title',
    sortDir: 'asc',
    filters: { downloaded: true, unread: true, completed: false, needsSource: true, stalled: false },
  },
}
