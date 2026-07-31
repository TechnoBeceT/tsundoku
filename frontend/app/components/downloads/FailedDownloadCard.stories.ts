import type { Meta, StoryObj } from '@storybook/vue3'
import FailedDownloadCard from './FailedDownloadCard.vue'
import { failedItems } from '../../fixtures/downloads'

/**
 * Stories for FailedDownloadCard — the failed-tab row variant: the shared row
 * plus retry-count + next-attempt, a retry/reset button, and an expandable
 * last-error panel. Covers the retryable vs terminal label, the §16 in-flight
 * retry state, and the expanded error detail.
 */
const meta = {
  title: 'Downloads/FailedDownloadCard',
  component: FailedDownloadCard,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof FailedDownloadCard>

export default meta
type Story = StoryObj<typeof meta>

// A retryable (failed) row with a network error + next attempt.
const retryable = failedItems.find((i) => i.state === 'failed')!
// A terminal (permanently_failed) row — the button reads "Reset".
const terminal = failedItems.find((i) => i.state === 'permanently_failed')!
// A downloaded chapter whose UPGRADE source keeps failing (retryable, 3/5) — the
// honest-failures row that used to be invisible. Reads "Comix → Hive Scans · 3/5".
const upgradeFailure = failedItems.find((i) => i.state === 'downloaded' && i.retryable)!
// The terminal variant of the same — the upgrade target burned its whole budget (5/5).
const upgradeTerminal = failedItems.find((i) => i.state === 'downloaded' && i.terminal)!

/** Retryable failure — collapsed, button reads "Retry". */
export const Retryable: Story = {
  args: { item: retryable },
}

/** Terminal failure — the action is "Reset" instead of "Retry". */
export const Terminal: Story = {
  args: { item: terminal },
}

/**
 * Honest source-failure: a DOWNLOADED chapter whose upgrade target is failing. The
 * badge names the FAILING source (Hive Scans · 3/5), the meta reads the Upgrade →
 * target, and the error is the failing source's own message.
 */
export const UpgradeFailure: Story = {
  args: { item: upgradeFailure },
}

/** Terminal upgrade-source failure — the target burned its whole budget; button "Reset". */
export const UpgradeTerminal: Story = {
  args: { item: upgradeTerminal, expanded: true },
}

/** Retry in flight — the button spins and reads "Retrying…" (§16). */
export const Retrying: Story = {
  args: { item: retryable, retrying: true },
}

/** Expanded — the last-error detail panel is open. */
export const Expanded: Story = {
  args: { item: retryable, expanded: true },
}

/**
 * EARLY ACCESS (GAP-141): a chapter the source is withholding behind coins reaches
 * this tab because its state IS `failed`, yet nothing is broken. The whole card
 * stands down — no attempt badge, an accent-toned "Early access" pill instead of the
 * danger-toned one, and the state badge replaced by the countdown to the free date.
 * The verbatim upstream message is still there on expand.
 */
export const EarlyAccess: Story = {
  args: {
    item: {
      ...retryable,
      state: 'failed',
      isUpgrade: false,
      upgradeTarget: undefined,
      failingProvider: undefined,
      failingProviderName: undefined,
      failingAttempts: 0,
      failingLastError: undefined,
      failingErrorCategory: undefined,
      attempts: 0,
      retryable: false,
      terminal: false,
      nextAttempt: undefined,
      lastError: 'upstream error: Chapter locked, coins required',
      locked: true,
      lockedUntil: new Date(Date.now() + 3 * 24 * 3_600_000).toISOString(),
    },
    expanded: true,
  },
}
