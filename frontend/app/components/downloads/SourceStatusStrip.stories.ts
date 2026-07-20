import type { Meta, StoryObj } from '@storybook/vue3'
import SourceStatusStrip from './SourceStatusStrip.vue'
import type { SourceStatus } from './sourceStatus.types'

/**
 * Stories for the live source-status strip — a wrapping row of source pills. The
 * Empty story renders nothing (an idle library shows no strip).
 */
const sources: SourceStatus[] = [
  { sourceKey: 'Asura Scans', state: 'downloading', activeCount: 5, cap: 5, cooldownRemainingSeconds: 0, reason: '', consecutiveFailures: 0, lastError: '' },
  { sourceKey: 'KaliScan', state: 'downloading', activeCount: 3, cap: 5, cooldownRemainingSeconds: 0, reason: '', consecutiveFailures: 0, lastError: '' },
  { sourceKey: 'Comix', state: 'cooling', activeCount: 0, cap: 5, cooldownRemainingSeconds: 720, reason: 'rate_limit', consecutiveFailures: 6, lastError: '429 rate limit exceeded' },
  { sourceKey: 'The Blank', state: 'cooling', activeCount: 0, cap: 5, cooldownRemainingSeconds: 240, reason: 'server_error', consecutiveFailures: 4, lastError: 'internal server error (503)' },
]

const meta = {
  title: 'Downloads/SourceStatusStrip',
  component: SourceStatusStrip,
  args: { sources },
} satisfies Meta<typeof SourceStatusStrip>

export default meta
type Story = StoryObj<typeof meta>

/** A mixed strip: two downloading + two cooling sources. */
export const Mixed: Story = {}

/** A single downloading source. */
export const Single: Story = {
  args: { sources: [sources[0]!] },
}

/** Idle — the strip renders nothing. */
export const Empty: Story = {
  args: { sources: [] },
}
