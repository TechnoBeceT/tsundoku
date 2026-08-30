import type { Meta, StoryObj } from '@storybook/vue3'
import SourceExceptionSummaryRow from './SourceExceptionSummaryRow.vue'
import {
  errorSourceException,
  longNameSourceException,
  pendingSourceException,
} from '../../fixtures/settings'
import '../../assets/css/tokens/settings.css'

/** One compact entry in the exception-first source rail. */
const meta = {
  title: 'Settings/SourceExceptionSummaryRow',
  component: SourceExceptionSummaryRow,
  parameters: { layout: 'padded' },
  decorators: [
    () => ({ template: '<div style="width:min(100%,760px)"><story /></div>' }),
  ],
  args: {
    source: pendingSourceException.source,
    exceptionCount: pendingSourceException.exceptionCount,
    runtime: pendingSourceException.runtime,
    selected: false,
    highlighted: false,
  },
} satisfies Meta<typeof SourceExceptionSummaryRow>

export default meta
type Story = StoryObj<typeof meta>

/** A pending source with six explicit settings. */
export const Pending: Story = {}

/** The selected source anchors the focused editor below the rail. */
export const Selected: Story = {
  args: { selected: true },
}

/** A source found through catalog search can be fully inherited. */
export const FullyInherited: Story = {
  args: {
    source: { sourceId: '2499283573021220255', name: 'MangaDex', language: 'en' },
    exceptionCount: 0,
    runtime: null,
  },
}

/** The latest sanitized apply diagnosis stays concise in the rail. */
export const SanitizedError: Story = {
  args: {
    source: errorSourceException.source,
    exceptionCount: errorSourceException.exceptionCount,
    runtime: errorSourceException.runtime,
  },
}

/** External navigation highlights, focuses, and scrolls this row into view. */
export const Highlighted: Story = {
  args: {
    source: longNameSourceException.source,
    exceptionCount: longNameSourceException.exceptionCount,
    runtime: longNameSourceException.runtime,
    highlighted: true,
  },
}

/** Long source names wrap without widening a narrow settings screen. */
export const Narrow: Story = {
  parameters: { viewport: { defaultViewport: 'mobile1' } },
  args: {
    source: longNameSourceException.source,
    exceptionCount: longNameSourceException.exceptionCount,
    runtime: longNameSourceException.runtime,
  },
}
