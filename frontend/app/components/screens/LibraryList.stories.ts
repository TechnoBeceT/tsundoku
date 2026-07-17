import type { Meta, StoryObj } from '@storybook/vue3'
import { computed, ref } from 'vue'
import LibraryList from './LibraryList.vue'
import type { SortKey, SortDir } from '../library/librarySort'
import { applyFilters, NO_FILTERS, type LibraryFilters } from '../library/libraryFilter'
import { categories, seriesPage } from '../../fixtures/series'

/**
 * Stories for the library grid — the first full screen, proving the token
 * foundation. Flip the Storybook theme toolbar to confirm it reads correctly in
 * BOTH dark and light. `Default` is interactive (category tabs + the search/sort
 * toolbar all filter local state, no refetch); the three `Empty*` stories cover
 * the honest empty-state branch, and `Loading` shows the skeleton grid.
 */
const meta = {
  title: 'Screens/LibraryList',
  component: LibraryList,
  parameters: { layout: 'fullscreen' },
  // series/categories/search/sort/filters/matchesElsewhere are required props;
  // the interactive story passes its own via the render template, so these
  // defaults only satisfy the CSF3 story typing.
  args: {
    series: seriesPage,
    categories,
    search: '',
    sortKey: 'title',
    sortDir: 'asc',
    filters: { ...NO_FILTERS },
    matchesElsewhere: 0,
  },
} satisfies Meta<typeof LibraryList>

export default meta
type Story = StoryObj<typeof meta>

/**
 * A healthy, varied page — covers + placeholders, paused/completed flags, fresh
 * 0% series, wanted/failed counts. Wired to local state so the category tabs and
 * the search + sort toolbar all work in-memory (the Komikku model).
 */
export const Default: Story = {
  render: () => ({
    components: { LibraryList },
    setup() {
      const activeCategory = ref<string | null>(null)
      const search = ref('')
      const sortKey = ref<SortKey>('title')
      const sortDir = ref<SortDir>('asc')
      const filters = ref<LibraryFilters>({ ...NO_FILTERS })

      const inCategory = computed(() =>
        activeCategory.value == null
          ? seriesPage
          : seriesPage.filter((s) => s.category === activeCategory.value),
      )
      const q = computed(() => search.value.trim().toLowerCase())
      const searched = computed(() =>
        q.value === ''
          ? inCategory.value
          : inCategory.value.filter((s) => s.title.toLowerCase().includes(q.value)),
      )
      const page = computed(() => applyFilters(searched.value, filters.value))
      const matchesElsewhere = computed(() =>
        q.value === ''
          ? 0
          : seriesPage.filter(
              (s) => s.category !== activeCategory.value && s.title.toLowerCase().includes(q.value),
            ).length,
      )

      const onFilter = (c: string | null): void => {
        activeCategory.value = c
      }
      const onSort = (p: { key: SortKey; dir: SortDir }): void => {
        sortKey.value = p.key
        sortDir.value = p.dir
      }
      const onFilters = (f: LibraryFilters): void => {
        filters.value = f
      }
      const onWiden = (): void => {
        activeCategory.value = null
      }

      return {
        activeCategory, search, sortKey, sortDir, filters,
        page, matchesElsewhere, categories, onFilter, onSort, onFilters, onWiden,
      }
    },
    template: `
      <LibraryList
        :series="page"
        :categories="categories"
        :active-category="activeCategory"
        :matches-elsewhere="matchesElsewhere"
        v-model:search="search"
        :filters="filters"
        :sort-key="sortKey"
        :sort-dir="sortDir"
        @filter="onFilter"
        @update:sort="onSort"
        @update:filters="onFilters"
        @search-everywhere="onWiden"
      />
    `,
  }),
}

/** The genuine category-empty state (no search active). */
export const EmptyCategory: Story = {
  args: {
    series: [],
    categories,
    activeCategory: 'Comic',
    search: '',
    matchesElsewhere: 0,
  },
}

/** A search that matches nothing anywhere in the library. */
export const NoSearchMatch: Story = {
  args: {
    series: [],
    categories,
    activeCategory: null,
    search: 'zzzzz',
    matchesElsewhere: 0,
  },
}

/** A search with no matches HERE but some elsewhere — the widen escape hatch. */
export const MatchesElsewhere: Story = {
  args: {
    series: [],
    categories,
    activeCategory: 'Manga',
    search: 'solo',
    matchesElsewhere: 1,
  },
}

/** Skeleton grid while the library loads. */
export const Loading: Story = {
  args: {
    series: [],
    categories,
    activeCategory: null,
    search: '',
    matchesElsewhere: 0,
    loading: true,
  },
}

/** A toggle-filter is active and nothing in view matches it. */
export const FilteredEmpty: Story = {
  args: {
    series: [],
    categories,
    activeCategory: null,
    search: '',
    filters: { ...NO_FILTERS, needsSource: true },
    matchesElsewhere: 0,
  },
}

/** The "Needs source" filter is active and matches a subset of the grid — only
 * the fixture entry that carries `needsSource: true` (see `fixtures/series.ts`,
 * which is ALSO the entry with a real cover — the cover-independence proof). */
export const NeedsSourceActive: Story = {
  args: {
    series: seriesPage.filter((s) => s.needsSource),
    categories,
    activeCategory: null,
    search: '',
    filters: { ...NO_FILTERS, needsSource: true },
    matchesElsewhere: 0,
  },
}
