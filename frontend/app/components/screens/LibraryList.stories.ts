import type { Meta, StoryObj } from '@storybook/vue3'
import { computed, ref } from 'vue'
import LibraryList from './LibraryList.vue'
import { categories, seriesPage } from '../../fixtures/series'

/**
 * Stories for the library grid — the first full screen, proving the token
 * foundation. Flip the Storybook theme toolbar to confirm it reads correctly in
 * BOTH dark and light. `Default` is interactive (the category tabs filter and
 * "Load more" appends), `Empty` shows the branded empty state, and `Loading`
 * shows the skeleton grid.
 */
const meta = {
  title: 'Screens/LibraryList',
  component: LibraryList,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof LibraryList>

export default meta
type Story = StoryObj<typeof meta>

/**
 * A healthy, varied page — covers + placeholders, paused/completed flags, fresh
 * 0% series, wanted/failed counts. Wired to local state so the filter tabs and
 * "Load more" pagination actually work (starts with 6 of 8 shown).
 */
export const Default: Story = {
  render: () => ({
    components: { LibraryList },
    setup() {
      const activeCategory = ref<string | null>(null)
      const shown = ref(6)

      const filtered = computed(() =>
        activeCategory.value == null
          ? seriesPage
          : seriesPage.filter((s) => s.category === activeCategory.value),
      )
      const page = computed(() => filtered.value.slice(0, shown.value))
      const total = computed(() => filtered.value.length)

      const onFilter = (c: string | null): void => {
        activeCategory.value = c
        shown.value = 6
      }
      const onLoadMore = (): void => {
        shown.value += 6
      }

      return { activeCategory, page, total, categories, onFilter, onLoadMore }
    },
    template: `
      <LibraryList
        :series="page"
        :categories="categories"
        :active-category="activeCategory"
        :total="total"
        @filter="onFilter"
        @load-more="onLoadMore"
      />
    `,
  }),
}

/** The styled empty state shown when a category has no series. */
export const Empty: Story = {
  args: {
    series: [],
    categories,
    activeCategory: 'Comic',
    total: 0,
  },
}

/** Skeleton grid while the first page loads. */
export const Loading: Story = {
  args: {
    series: [],
    categories,
    activeCategory: null,
    total: 0,
    loading: true,
  },
}
