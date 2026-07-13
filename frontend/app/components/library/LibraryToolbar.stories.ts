import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import LibraryToolbar from './LibraryToolbar.vue'
import type { SortKey, SortDir } from './librarySort'

/**
 * Stories for the library search + sort toolbar. `Default` is interactive (the
 * search field + the sort dropdown drive local state); the remaining stories pin
 * each sort option's selected state. Flip the Storybook theme toolbar to confirm
 * the input + native select read in BOTH dark and light.
 */
const meta = {
  title: 'Library/LibraryToolbar',
  component: LibraryToolbar,
  // search/sortKey/sortDir are required props; the interactive Default overrides
  // them with local refs, so these defaults only satisfy the CSF3 story typing.
  args: { search: '', sortKey: 'title', sortDir: 'asc' },
} satisfies Meta<typeof LibraryToolbar>

export default meta
type Story = StoryObj<typeof meta>

/** Interactive — typing filters and the dropdown re-sorts local state. */
export const Default: Story = {
  render: () => ({
    components: { LibraryToolbar },
    setup() {
      const search = ref('')
      const sortKey = ref<SortKey>('title')
      const sortDir = ref<SortDir>('asc')
      const onSort = (p: { key: SortKey; dir: SortDir }): void => {
        sortKey.value = p.key
        sortDir.value = p.dir
      }
      return { search, sortKey, sortDir, onSort }
    },
    template: `
      <div style="max-width:720px">
        <LibraryToolbar
          v-model:search="search"
          :sort-key="sortKey"
          :sort-dir="sortDir"
          @update:sort="onSort"
        />
      </div>
    `,
  }),
}

/** A search in progress — the clear × shows and the sort is Title A–Z. */
export const Searching: Story = {
  args: { search: 'solo leveling', sortKey: 'title', sortDir: 'asc' },
}

/** Title Z–A selected. */
export const TitleDescending: Story = {
  args: { search: '', sortKey: 'title', sortDir: 'desc' },
}

/** Recently added selected. */
export const RecentlyAdded: Story = {
  args: { search: '', sortKey: 'added', sortDir: 'desc' },
}

/** Recently updated selected. */
export const RecentlyUpdated: Story = {
  args: { search: '', sortKey: 'updated', sortDir: 'desc' },
}

/** Most unread selected. */
export const MostUnread: Story = {
  args: { search: '', sortKey: 'unread', sortDir: 'desc' },
}
