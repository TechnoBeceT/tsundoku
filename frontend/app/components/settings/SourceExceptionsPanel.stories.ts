import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, fn, userEvent, within } from 'storybook/test'
import SourceExceptionsPanel from './SourceExceptionsPanel.vue'
import {
  comicAsuraSourceConfiguration,
  errorSourceException,
  fullyInheritedSourceConfiguration,
  hiveProxySourceConfiguration,
  longNameSourceException,
  networkEndpoints,
  pendingSourceException,
} from '../../fixtures/settings'
import type { components } from '../../utils/api/schema.d.ts'
import '../../assets/css/tokens/settings.css'

type SourceExceptionSummary = components['schemas']['SourceExceptionSummary']

const comicAsuraSummary = {
  source: comicAsuraSourceConfiguration.source,
  exceptionCount: 6,
  runtime: comicAsuraSourceConfiguration.runtime,
} satisfies SourceExceptionSummary

const hiveSummary = {
  source: hiveProxySourceConfiguration.source,
  exceptionCount: 1,
  runtime: hiveProxySourceConfiguration.runtime,
} satisfies SourceExceptionSummary

const summaries = [comicAsuraSummary, hiveSummary, errorSourceException]
const sources = [
  fullyInheritedSourceConfiguration.source,
  comicAsuraSourceConfiguration.source,
  hiveProxySourceConfiguration.source,
  errorSourceException.source,
  longNameSourceException.source,
]

const baseArgs = {
  sources,
  summaries,
  selectedSourceId: comicAsuraSourceConfiguration.source.sourceId,
  configuration: comicAsuraSourceConfiguration,
  endpoints: networkEndpoints,
  globalDownloadConcurrency: 5,
  globalImageRequestDelay: '500ms',
  pending: false,
  catalogPending: false,
  catalogLoaded: true,
  catalogError: null,
  configurationPending: false,
  configurationError: null,
  highlightedSourceId: null,
  action: { sourceId: null, key: null, saving: false, error: null },
}

/**
 * The canonical source-exception editor: one exception-count/status rail and
 * one selected source editor. All data and persistence state arrive via props.
 */
const meta = {
  title: 'Settings/SourceExceptionsPanel',
  component: SourceExceptionsPanel,
  parameters: { layout: 'padded' },
  args: baseArgs,
} satisfies Meta<typeof SourceExceptionsPanel>

export default meta
type Story = StoryObj<typeof meta>

/** Three exception-bearing sources, nine explicit settings, and one pending apply. */
export const OverviewCounts: Story = {}

/** Search reaches beyond the exception overview and finds MangaDex. */
export const SearchFindsFullyInherited: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.type(canvas.getByPlaceholderText('Search every installed source'), 'MangaDex')
    await expect(canvas.getByRole('button', { name: /MangaDex/ })).toBeVisible()
    await expect(canvas.getByText('Inherits all settings')).toBeVisible()
  },
}

/** A selected source can inherit every mutable setting while showing all effective values. */
export const SelectedInherited: Story = {
  args: {
    selectedSourceId: fullyInheritedSourceConfiguration.source.sourceId,
    configuration: fullyInheritedSourceConfiguration,
  },
}

/** Comic Asura shows conservative pace, disposable sessions, fresh images, and VPN routes. */
export const ComicAsuraOverrides: Story = {}

/** Hive Scans keeps transport inherited but explicitly opts into the image proxy. */
export const HiveProxyOptIn: Story = {
  args: {
    selectedSourceId: hiveProxySourceConfiguration.source.sourceId,
    configuration: hiveProxySourceConfiguration,
  },
}

/** Desired revision 19 is still converging from applied revision 18. */
export const Pending: Story = {
  args: {
    summaries: [pendingSourceException, hiveSummary, errorSourceException],
    configuration: {
      ...comicAsuraSourceConfiguration,
      runtime: pendingSourceException.runtime,
    },
  },
}

/** Advanced diagnostics show only the approved fields and the sanitized error. */
export const SanitizedError: Story = {
  args: {
    selectedSourceId: errorSourceException.source.sourceId,
    configuration: {
      ...fullyInheritedSourceConfiguration,
      source: errorSourceException.source,
      profileKey: 'default',
      runtime: errorSourceException.runtime,
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByText('Advanced diagnostics'))
    await expect(canvas.getByText(errorSourceException.runtime.lastApplyError)).toBeVisible()
  },
}

/** Catalog and summary rail loading state. */
export const Loading: Story = {
  args: {
    sources: [],
    summaries: [],
    configuration: null,
    pending: true,
    catalogPending: true,
    catalogLoaded: false,
  },
}

/** No extensions means there is no source catalog to search or edit. */
export const EmptyCatalog: Story = {
  args: {
    sources: [],
    summaries: [],
    configuration: null,
    selectedSourceId: null,
    catalogLoaded: true,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByText('No sources installed')).toBeVisible()
    await expect(canvas.queryByRole('searchbox')).not.toBeInTheDocument()
    await expect(canvas.queryByLabelText('Source exception overview')).not.toBeInTheDocument()
  },
}

/** A failed catalog refresh keeps the last confirmed catalog available behind a retry. */
export const PreservedCatalogRetry: Story = {
  args: {
    catalogLoaded: true,
    catalogError: 'The installed source catalog could not be refreshed. Try again.',
    'onRetry-catalog': fn(),
  },
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByText('The installed source catalog could not be refreshed. Try again.')).toBeVisible()
    await expect(canvas.getByRole('button', { name: /Comic Asura/ })).toBeVisible()
    await expect(canvas.queryByText('No sources installed')).not.toBeInTheDocument()
    await userEvent.click(canvas.getByRole('button', { name: 'Retry source catalog' }))
    await expect(args['onRetry-catalog']).toHaveBeenCalledOnce()
  },
}

/** A query with no catalog match gives a specific, recoverable empty result. */
export const NoSearchResults: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.type(canvas.getByPlaceholderText('Search every installed source'), 'not installed')
    await expect(canvas.getByText('No sources match “not installed”.')).toBeVisible()
  },
}

/** External navigation brings the long row into keyboard focus. */
export const HighlightedRow: Story = {
  args: { highlightedSourceId: longNameSourceException.source.sourceId },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByRole('button', { name: new RegExp(longNameSourceException.source.name) })).toHaveFocus()
  },
}

/** The rail and complete editor stack without horizontal overflow on phones. */
export const NarrowLayout: Story = {
  parameters: { viewport: { defaultViewport: 'mobile1' } },
  args: {
    selectedSourceId: longNameSourceException.source.sourceId,
    configuration: {
      ...fullyInheritedSourceConfiguration,
      source: longNameSourceException.source,
      profileKey: 'archive-mirror-with-a-deliberately-long-profile-key',
    },
  },
  play: async ({ canvasElement }) => {
    const panel = canvasElement.querySelector<HTMLElement>('.source-exceptions')
    await expect(panel).not.toBeNull()
    await expect(panel!.scrollWidth).toBeLessThanOrEqual(panel!.clientWidth)
  },
}
