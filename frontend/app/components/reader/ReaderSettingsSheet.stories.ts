import type { Meta, StoryObj } from '@storybook/vue3'
import { reactive, ref } from 'vue'
import ReaderSettingsSheet from './ReaderSettingsSheet.vue'
import { READER_SETTINGS_DEFAULTS, type ReaderSettings } from '~/composables/useReaderSettings'

/**
 * Stories for the reader-settings sheet. Each story wires a LOCAL reactive
 * settings object + open ref so the sliders/toggles actually move — mirroring how
 * the reader route feeds `useReaderSettings` down and applies the emitted patches.
 * Flip the Storybook theme toolbar to check both looks.
 */
const meta = {
  title: 'Reader/ReaderSettingsSheet',
  component: ReaderSettingsSheet,
  parameters: { layout: 'fullscreen' },
  // Satisfies the required props at the meta level; the interactive stories drive
  // their own local state via `render`, so these baseline args are unused.
  args: { open: true, settings: READER_SETTINGS_DEFAULTS },
} satisfies Meta<typeof ReaderSettingsSheet>

export default meta
type Story = StoryObj<typeof meta>

/** Shared interactive template: a live settings object the sheet edits in place. */
const interactive = (initial: ReaderSettings) => () => ({
  components: { ReaderSettingsSheet },
  setup() {
    const open = ref(true)
    const settings = reactive({ ...initial })
    const onChange = (patch: Partial<ReaderSettings>) => Object.assign(settings, patch)
    return { open, settings, onChange }
  },
  template: `
    <div style="height:520px">
      <ReaderSettingsSheet v-model:open="open" :settings="settings" @change="onChange" />
    </div>
  `,
})

/** The default settings (capped column, no padding, gaps off). */
export const Defaults: Story = { render: interactive(READER_SETTINGS_DEFAULTS) }

/** Full-width fit — the max-width slider hides, padding + gaps are on. */
export const FullWidth: Story = {
  render: interactive({ sidePaddingPct: 8, fit: 'width', maxWidthPx: 800, gaps: true }),
}

/** A wide capped column with gaps — the max-width slider is shown. */
export const WideCapped: Story = {
  render: interactive({ sidePaddingPct: 4, fit: 'max', maxWidthPx: 1200, gaps: true }),
}
