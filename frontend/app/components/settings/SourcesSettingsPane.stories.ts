import type { Meta, StoryObj } from '@storybook/vue3'
import SourcesSettingsPane from './SourcesSettingsPane.vue'
import { sourcesSettings } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the actions that stay outside Download engine: library-wide
 * maintenance and source-scoped bulk re-download.
 */
const meta = {
  title: 'Settings/SourcesSettingsPane',
  component: SourcesSettingsPane,
  parameters: { layout: 'padded' },
  args: {
    sources: sourcesSettings,
  },
} satisfies Meta<typeof SourcesSettingsPane>

export default meta
type Story = StoryObj<typeof meta>

/** Both owner-triggered actions remain available, but not inside Source exceptions. */
export const Default: Story = {}

/** A finished dedup sweep reports a busy series separately. */
export const DedupSkippedBusy: Story = {
  args: { dedupAllMessage: 'Provider deduplication finished.', dedupAllSkippedBusy: 1 },
}
