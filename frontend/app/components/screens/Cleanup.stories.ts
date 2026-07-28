import type { Meta, StoryObj } from '@storybook/vue3'
import Cleanup from './Cleanup.vue'
import { fractionalSeries } from '../../fixtures/fractionals'
import { sampleSourcelessSeries } from '../../fixtures/sourceless'
import { sampleDuplicateSeries } from '../../fixtures/duplicates'
import type {
  CleanupFractionalsPane,
  CleanupSourcelessPane,
  CleanupDuplicatesPane,
} from './cleanup.types'

/**
 * Stories for the `/cleanup` console — the 3-tab screen folding Fractionals,
 * Sourceless and Duplicates into one page. The active tab is CONTROLLED, so each
 * story pins one tab; the page owns the real tab state, its `?tab=` deep-link and
 * its sessionStorage persistence. Every tab body is a screen that already has its
 * own stories — this shell only composes them. Flip the theme toolbar to confirm
 * both themes read.
 */
const fractionals: CleanupFractionalsPane = {
  series: fractionalSeries,
  loading: false,
  refreshing: false,
  error: null,
  busyIds: [],
  toggleError: null,
}

const sourceless: CleanupSourcelessPane = {
  series: sampleSourcelessSeries,
  loading: false,
  refreshing: false,
  error: null,
}

const duplicates: CleanupDuplicatesPane = {
  series: sampleDuplicateSeries,
  totalFiles: 258,
  totalBytes: 1_311_810_518,
  loading: false,
  refreshing: false,
  error: null,
}

const meta = {
  title: 'Screens/Cleanup',
  component: Cleanup,
  parameters: { layout: 'fullscreen' },
  args: { fractionals, sourceless, duplicates },
} satisfies Meta<typeof Cleanup>

export default meta
type Story = StoryObj<typeof meta>

/** The default tab — Fractionals. */
export const FractionalsTab: Story = {
  args: { activeTab: 'fractionals' },
}

/** The Sourceless tab (what `/sourceless` now redirects to). */
export const SourcelessTab: Story = {
  args: { activeTab: 'sourceless' },
}

/** The Duplicates tab — the new discovery surface. */
export const DuplicatesTab: Story = {
  args: { activeTab: 'duplicates' },
}

/** A tab whose data has not loaded yet — skeletons, no banner. */
export const DuplicatesLoading: Story = {
  args: {
    activeTab: 'duplicates',
    duplicates: { ...duplicates, series: [], totalFiles: 0, totalBytes: 0, loading: true },
  },
}

/** A failed load — the banner sits above the tab body (§16). */
export const DuplicatesError: Story = {
  args: {
    activeTab: 'duplicates',
    duplicates: { ...duplicates, series: [], error: 'Failed to load duplicate files' },
  },
}
