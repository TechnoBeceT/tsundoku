import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, within } from 'storybook/test'
import DownloadEnginePane from './DownloadEnginePane.vue'
import {
  comicAsuraSourceConfiguration,
  errorSourceException,
  flareSolverrConfig,
  fullyInheritedSourceConfiguration,
  hiveProxySourceConfiguration,
  impersonateConfig,
  librarySettings,
  longNameSourceException,
  networkEndpoints,
  sourcesSettings,
} from '../../fixtures/settings'
import type { components } from '../../utils/api/schema.d.ts'
import '../../assets/css/tokens/settings.css'

type SourceExceptionSummary = components['schemas']['SourceExceptionSummary']

const summaries = [
  {
    source: comicAsuraSourceConfiguration.source,
    exceptionCount: 6,
    runtime: comicAsuraSourceConfiguration.runtime,
  },
  {
    source: hiveProxySourceConfiguration.source,
    exceptionCount: 1,
    runtime: hiveProxySourceConfiguration.runtime,
  },
  errorSourceException,
] satisfies SourceExceptionSummary[]

const sourceCatalog = [
  fullyInheritedSourceConfiguration.source,
  comicAsuraSourceConfiguration.source,
  hiveProxySourceConfiguration.source,
  errorSourceException.source,
  longNameSourceException.source,
]

const baseArgs = {
  library: librarySettings,
  sources: sourcesSettings,
  flareSolverr: flareSolverrConfig,
  impersonate: impersonateConfig,
  endpoints: networkEndpoints,
  sourceCatalog,
  sourceSummaries: summaries,
  selectedSourceId: comicAsuraSourceConfiguration.source.sourceId,
  sourceConfiguration: comicAsuraSourceConfiguration,
}

/**
 * Global download behavior and the one canonical source-exception editor share
 * a single anchored flow. Library cleanup and engine lifecycle diagnostics are
 * intentionally absent because their ownership remains outside this pane.
 */
const meta = {
  title: 'Settings/DownloadEnginePane',
  component: DownloadEnginePane,
  parameters: { layout: 'padded' },
  args: baseArgs,
} satisfies Meta<typeof DownloadEnginePane>

export default meta
type Story = StoryObj<typeof meta>

/** Desktop: the section spine stays beside all five canonical sections. */
export const Desktop: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    for (const heading of ['Scheduling', 'Protection', 'Access & bypass', 'Routing', 'Source exceptions']) {
      await expect(canvas.getByRole('heading', { name: heading, level: 2 })).toBeVisible()
    }
    await expect(canvas.getByRole('button', { name: 'Add endpoint' })).toBeVisible()
  },
}

/** Narrow: spine and sections stack, long source values wrap, and no pane overflows. */
export const Narrow: Story = {
  parameters: { viewport: { defaultViewport: 'mobile1' } },
  args: {
    selectedSourceId: longNameSourceException.source.sourceId,
    sourceConfiguration: {
      ...fullyInheritedSourceConfiguration,
      source: longNameSourceException.source,
      profileKey: 'archive-mirror-with-a-deliberately-long-profile-key',
    },
  },
  play: async ({ canvasElement }) => {
    const pane = canvasElement.querySelector<HTMLElement>('[data-testid="download-engine-pane"]')
    await expect(pane).not.toBeNull()
    await expect(pane!.querySelectorAll('[data-engine-section]')).toHaveLength(5)
    await expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(document.documentElement.clientWidth)
    await expect(pane!.scrollWidth).toBeLessThanOrEqual(pane!.clientWidth)
  },
}
