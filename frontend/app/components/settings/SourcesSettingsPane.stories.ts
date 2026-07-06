import type { Meta, StoryObj } from '@storybook/vue3'
import SourcesSettingsPane from './SourcesSettingsPane.vue'
import { sourcesSettings, sourcesSettingsWarmupDisabled } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Sources settings pane (warm-up cadence/threshold + the
 * per-source circuit-breaker/politeness-delay knobs — the source-politeness
 * spec). Flip the Storybook theme toolbar to confirm both dark and light.
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

/** Idle — nothing edited yet, so the Save button is disabled. */
export const Default: Story = {
  args: { save: { status: 'idle' } },
}

/** Warm-up disabled (0) — the state recommended when a source keeps getting IP-blocked. */
export const WarmupDisabled: Story = {
  args: { sources: sourcesSettingsWarmupDisabled, save: { status: 'idle' } },
}

/** §16 saving — the Save button spins while the persist call is in flight. */
export const Saving: Story = {
  args: { save: { status: 'saving' } },
}

/** §16 error — a visible, specific failure message beside the Save button. */
export const SaveError: Story = {
  args: { save: { status: 'error', message: 'Save failed — sources.failure_threshold must be in [1, 100] (got 0).' } },
}
