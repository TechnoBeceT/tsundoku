import type { Meta, StoryObj } from '@storybook/vue3'
import { computed, ref } from 'vue'
import Discover from './Discover.vue'
import type { BrowseType } from './discover.types'
import { latestResult, popularResult, sources } from '../../fixtures/discover'

/**
 * Stories for the Discover browse screen. Flip the Storybook theme toolbar to
 * confirm it reads correctly in BOTH dark and light, and hover a card to confirm
 * the preview popup renders solidly (anchored, never clipped, no flicker).
 * `Default` is interactive (the source picker + Popular/Latest toggle swap the
 * grid); `Latest`, `Loading`, and `Empty` show the remaining states.
 */
const meta = {
  title: 'Screens/Discover',
  component: Discover,
  parameters: { layout: 'fullscreen' },
  // result/sources/activeSource are required props; the interactive stories pass
  // their own in the render template, so these defaults only satisfy CSF3 typing.
  args: { result: popularResult, sources, activeSource: sources[0]!.id },
} satisfies Meta<typeof Discover>

export default meta
type Story = StoryObj<typeof meta>

/**
 * A populated Popular grid wired to local state, so the Popular/Latest toggle and
 * source picker actually swap the results (Popular has a next page; Latest does
 * not, so it shows the "End of list" state).
 */
export const Default: Story = {
  render: () => ({
    components: { Discover },
    setup() {
      const activeSource = ref(sources[0]!.id)
      const activeType = ref<BrowseType>('popular')

      const result = computed(() => (activeType.value === 'popular' ? popularResult : latestResult))

      const onType = (t: BrowseType): void => {
        activeType.value = t
      }
      const onSource = (id: string): void => {
        activeSource.value = id
      }

      return { result, sources, activeSource, activeType, onType, onSource }
    },
    template: `
      <Discover
        :result="result"
        :sources="sources"
        :active-source="activeSource"
        :active-type="activeType"
        @set-type="onType"
        @set-source="onSource"
      />
    `,
  }),
}

/** The Latest listing — a complete (last) page, so it ends with "End of list". */
export const Latest: Story = {
  args: {
    result: latestResult,
    sources,
    activeSource: sources[0]!.id,
    activeType: 'latest',
  },
}

/** Skeleton grid while the first page loads. */
export const Loading: Story = {
  args: {
    result: { manga: [], hasNextPage: false, page: 1 },
    sources,
    activeSource: sources[0]!.id,
    activeType: 'popular',
    loading: true,
  },
}

/** The source returned nothing for this listing. */
export const Empty: Story = {
  args: {
    result: { manga: [], hasNextPage: false, page: 1 },
    sources,
    activeSource: sources[0]!.id,
    activeType: 'popular',
  },
}

/** The active source failed — error banner with a retry affordance. */
export const ErrorState: Story = {
  args: {
    result: { manga: [], hasNextPage: false, page: 1 },
    sources,
    activeSource: sources[0]!.id,
    activeType: 'popular',
    error: true,
  },
}
