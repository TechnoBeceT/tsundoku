import type { Meta, StoryObj } from '@storybook/vue3'
import SourceStatusRow from './SourceStatusRow.vue'
import type { SourceStatus } from './sourceStatus.types'

/**
 * Stories for a single source-status pill — one downloading, one cooling. Flip the
 * Storybook theme toolbar to confirm the accent (downloading) + amber (cooling)
 * dots read on both surfaces.
 */
const downloading: SourceStatus = {
  sourceKey: 'Asura Scans',
  state: 'downloading',
  activeCount: 5,
  cap: 5,
  cooldownRemainingSeconds: 0,
  reason: '',
  consecutiveFailures: 0,
  lastError: '',
}

const cooling: SourceStatus = {
  sourceKey: 'Comix',
  state: 'cooling',
  activeCount: 0,
  cap: 5,
  cooldownRemainingSeconds: 720,
  reason: 'rate_limit',
  consecutiveFailures: 6,
  lastError: '429 rate limit exceeded',
}

const meta = {
  title: 'Downloads/SourceStatusRow',
  component: SourceStatusRow,
  args: { source: downloading },
} satisfies Meta<typeof SourceStatusRow>

export default meta
type Story = StoryObj<typeof meta>

/** An actively-downloading source: "Asura Scans ● downloading 5/5". */
export const Downloading: Story = {}

/** A cooling source: "Comix ⏸ cooling 12m (rate-limited)" (hover for the full error). */
export const Cooling: Story = {
  args: { source: cooling },
}

/** A cooling source on a server-error cooldown, minutes out. */
export const CoolingServerError: Story = {
  args: {
    source: { ...cooling, sourceKey: 'The Blank', cooldownRemainingSeconds: 240, reason: 'server_error', lastError: 'internal server error (503)' },
  },
}
