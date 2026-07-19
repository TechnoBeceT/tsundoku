import type { Meta, StoryObj } from '@storybook/vue3'
import { computed, ref } from 'vue'
import AppShell from './AppShell.vue'
import type { NavItem } from './types'
import LibraryList from '../screens/LibraryList.vue'
import type { SortKey, SortDir } from '../library/librarySort'
import { NO_FILTERS, type LibraryFilters } from '../library/libraryFilter'
import { categories, seriesPage } from '../../fixtures/series'

/**
 * Stories for the app chrome. `layout: 'fullscreen'` is MANDATORY — the shell
 * fills the viewport via a `height: 100%` / `min-height: 100vh` chain, and the
 * default `padded` layout collapses it to content height (a render-only bug).
 * Flip the Storybook theme toolbar to confirm the rail + header read correctly
 * in BOTH dark and light.
 */
/**
 * The six nav rail items — five primary, plus Settings bottom-pinned. Badges are
 * caller-wired (Downloads → failed count amber, Health → unhealthy rose), proving
 * the shell renders whatever badges it is handed.
 */
const navItems: NavItem[] = [
  { key: 'library', label: 'Library', icon: 'book' },
  { key: 'discover', label: 'Discover', icon: 'compass' },
  { key: 'downloads', label: 'Downloads', icon: 'download', badge: { count: 1, tone: 'warn' } },
  { key: 'health', label: 'Health', icon: 'activity', badge: { count: 3, tone: 'danger' } },
  { key: 'fractionals', label: 'Fractionals', icon: 'scissors' },
  { key: 'categories', label: 'Categories', icon: 'layout-grid' },
  { key: 'settings', label: 'Settings', icon: 'settings', pinned: true },
]

const meta = {
  title: 'Shell/AppShell',
  component: AppShell,
  parameters: { layout: 'fullscreen' },
  // navItems/activeRoute/theme/headerTitle are required props; each story passes
  // its own in the render template, so these defaults only satisfy CSF3 typing.
  args: { navItems, activeRoute: 'library', theme: 'dark', headerTitle: 'Library' },
} satisfies Meta<typeof AppShell>

export default meta
type Story = StoryObj<typeof meta>

/** A simple placeholder so the chrome can be judged without a real screen. */
const Placeholder = {
  template: `
    <div style="padding:24px 30px">
      <div style="background:var(--surface);border:1px solid var(--border);border-radius:18px;padding:40px;color:var(--muted)">
        Screen content renders here, inside the shell's <code>&lt;slot/&gt;</code>.
      </div>
    </div>
  `,
}

/**
 * The resting chrome: Library active, three sources need attention (header pill +
 * Health rail badge), one failed download (Downloads rail badge), two active
 * downloads (rail-bottom indicator), no sync in progress.
 */
export const Default: Story = {
  render: () => ({
    components: { AppShell, Placeholder },
    setup() {
      const theme = ref<'dark' | 'light'>('dark')
      return { theme, navItems }
    },
    template: `
      <AppShell
        :nav-items="navItems"
        active-route="library"
        :theme="theme"
        header-title="Library"
        :unhealthy="3"
        :active-downloads="2"
        @toggle-theme="theme = theme === 'dark' ? 'light' : 'dark'"
      >
        <Placeholder />
      </AppShell>
    `,
  }),
}

/**
 * A discovery sweep / download cycle in flight: the header spinner announces via
 * `aria-live`, the indeterminate mutation bar runs, and two downloads have failed
 * (rail-bottom amber indicator).
 */
export const Syncing: Story = {
  render: () => ({
    components: { AppShell, Placeholder },
    setup() {
      const theme = ref<'dark' | 'light'>('dark')
      return { theme, navItems }
    },
    template: `
      <AppShell
        :nav-items="navItems"
        active-route="downloads"
        :theme="theme"
        header-title="Downloads"
        :syncing="true"
        sync-label="Syncing sources…"
        :active-downloads="4"
        :failed-downloads="2"
        :mutating="true"
        @toggle-theme="theme = theme === 'dark' ? 'light' : 'dark'"
      >
        <Placeholder />
      </AppShell>
    `,
  }),
}

/**
 * The real proof: the LibraryList screen sitting inside the chrome, framed by the
 * rail + header exactly as it will appear in the running app. The category tabs
 * and the search + sort toolbar stay interactive (wired to local state, the
 * Komikku model — no refetch).
 */
export const InShell: Story = {
  render: () => ({
    components: { AppShell, LibraryList },
    setup() {
      const theme = ref<'dark' | 'light'>('dark')
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
      const page = computed(() =>
        q.value === ''
          ? inCategory.value
          : inCategory.value.filter((s) => s.title.toLowerCase().includes(q.value)),
      )
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
      const onWiden = (): void => {
        activeCategory.value = null
      }

      return {
        theme,
        navItems,
        activeCategory,
        search,
        sortKey,
        sortDir,
        filters,
        page,
        matchesElsewhere,
        categories,
        onFilter,
        onSort,
        onWiden,
      }
    },
    template: `
      <AppShell
        :nav-items="navItems"
        active-route="library"
        :theme="theme"
        header-title="Library"
        :unhealthy="3"
        :active-downloads="2"
        @toggle-theme="theme = theme === 'dark' ? 'light' : 'dark'"
      >
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
          @search-everywhere="onWiden"
        />
      </AppShell>
    `,
  }),
}
