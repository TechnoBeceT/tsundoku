import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, userEvent, within } from 'storybook/test'
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
  selectedSourceId: null,
  sourceConfiguration: null,
}

/**
 * Global download behavior and the one canonical source-exception editor share
 * one five-tab surface. Library cleanup and engine lifecycle diagnostics remain
 * outside this pane because they have separate ownership.
 */
const meta = {
  title: 'Settings/DownloadEnginePane',
  component: DownloadEnginePane,
  parameters: { layout: 'padded' },
  args: baseArgs,
} satisfies Meta<typeof DownloadEnginePane>

export default meta
type Story = StoryObj<typeof meta>

/** Desktop dark theme: Scheduling is the readable default tab. */
export const Desktop: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByRole('tab', { name: 'Scheduling' })).toHaveAttribute('aria-selected', 'true')
    await expect(canvas.getByRole('tabpanel', { name: 'Scheduling' })).toBeVisible()
  },
}

/** Light-theme reference for the same token-backed Scheduling composition. */
export const LightTheme: Story = {
  globals: { theme: 'light' },
  play: Desktop.play,
}

/** Protection keeps global anti-block and pacing controls together. */
export const Protection: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByRole('tab', { name: 'Protection' }))
    await expect(canvas.getByRole('tabpanel', { name: 'Protection' })).toBeVisible()
  },
}

/** Access and bypass keeps the two existing shared service components intact. */
export const AccessAndBypass: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByRole('tab', { name: 'Access & bypass' }))
    await expect(canvas.getByRole('tabpanel', { name: 'Access & bypass' })).toBeVisible()
  },
}

/** Routing preserves endpoint CRUD and its local dialog state. */
export const Routing: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByRole('tab', { name: 'Routing' }))
    await expect(canvas.getByRole('button', { name: 'Add endpoint' })).toBeVisible()
  },
}

/** A selected source deep link opens Source exceptions without changing its ID. */
export const SourceExceptions: Story = {
  args: {
    selectedSourceId: comicAsuraSourceConfiguration.source.sourceId,
    sourceConfiguration: comicAsuraSourceConfiguration,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByRole('tab', { name: 'Source exceptions' })).toHaveAttribute('aria-selected', 'true')
    await expect(canvas.getByRole('tabpanel', { name: 'Source exceptions' })).toBeVisible()
  },
}

/** A dirty Scheduling draft survives leaving and returning to its mounted panel. */
export const DirtyDraftSurvivesSwitch: Story = {
  args: {
    selectedSourceId: null,
    sourceConfiguration: null,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const retries = canvas.getByRole('spinbutton', { name: 'Chapter max retries' })
    await userEvent.clear(retries)
    await userEvent.type(retries, '12')
    await userEvent.click(canvas.getByRole('tab', { name: 'Protection' }))
    await userEvent.click(canvas.getByRole('tab', { name: 'Scheduling' }))
    await expect(canvas.getByRole('spinbutton', { name: 'Chapter max retries' })).toHaveValue(12)
    await expect(canvas.getByRole('button', { name: 'Save scheduling settings' })).toBeEnabled()
  },
}

/** Summary read failed locally; global controls and an explicit retry remain. */
export const SummaryFailure: Story = {
  args: {
    sourceSummaries: [],
    sourceSummariesError: 'Source exceptions could not be loaded.',
    selectedSourceId: null,
    sourceConfiguration: null,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByRole('tab', { name: 'Source exceptions' }))
    await expect(canvas.getByText('Source exceptions could not be loaded.')).toBeVisible()
    await expect(canvas.getByRole('button', { name: 'Retry exception list' })).toBeVisible()
    await expect(canvas.queryByText(/Every source currently inherits/)).toBeNull()
  },
}

/** Source exceptions keeps its explicit loading state inside the active tab. */
export const SourceExceptionsLoading: Story = {
  args: {
    selectedSourceId: null,
    sourceConfiguration: null,
    sourceExceptionsPending: true,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByRole('tab', { name: 'Source exceptions' }))
    await expect(canvas.getByRole('status', { name: 'Loading source exceptions' })).toBeVisible()
  },
}

/** Narrow: readable tabs wrap, long source values wrap, and no pane overflows. */
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
