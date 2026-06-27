import type { Meta, StoryObj } from '@storybook/vue3'
import SettingRow from './SettingRow.vue'
import DurationInput from '../ui/DurationInput.vue'
import TextField from '../ui/TextField.vue'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for SettingRow — the shared "label + hint + trailing control" line used
 * across the Library, Engine, and Extensions panes. Shown in a card-width frame;
 * flip the Storybook theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'Settings/SettingRow',
  component: SettingRow,
  argTypes: {
    name: { control: 'text' },
    hint: { control: 'text' },
    flush: { control: 'boolean' },
    spaced: { control: 'boolean' },
  },
  args: {
    name: 'Refresh interval',
    hint: 'How often to poll titles for new chapters',
  },
  decorators: [
    () => ({ template: '<div style="width:520px"><story /></div>' }),
  ],
  render: (args) => ({
    components: { SettingRow, DurationInput },
    setup: () => ({ args, model: { value: 2, unit: 'h' } }),
    template:
      '<SettingRow v-bind="args"><DurationInput :model-value="model" /></SettingRow>',
  }),
} satisfies Meta<typeof SettingRow>

export default meta
type Story = StoryObj<typeof meta>

/** A duration row — the most common shape (name + hint + DurationInput). */
export const Default: Story = {}

/** Name only — no hint line. */
export const NameOnly: Story = {
  args: { name: 'Running version', hint: undefined },
}

/** An integer field as the trailing control (the compact TextField variant). */
export const WithNumberField: Story = {
  args: { name: 'Chapter max retries', hint: 'Attempts before a chapter is permanently failed' },
  render: (args) => ({
    components: { SettingRow, TextField },
    setup: () => ({ args }),
    template:
      '<SettingRow v-bind="args"><TextField compact type="number" model-value="3" /></SettingRow>',
  }),
}

/** `flush` — no top border, tightened bottom (used inside the Advanced disclosure). */
export const Flush: Story = {
  args: { name: 'Refresh concurrency', hint: 'Parallel source fetches — be gentle on sources', flush: true },
}

/** `spaced` — extra top separation (the standalone update-check cadence row). */
export const Spaced: Story = {
  args: { name: 'Extension update check', hint: 'How often to auto-check for extension updates', spaced: true },
}
