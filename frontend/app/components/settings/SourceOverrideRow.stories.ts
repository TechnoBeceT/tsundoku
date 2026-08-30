import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, userEvent, within } from 'storybook/test'
import SourceOverrideRow from './SourceOverrideRow.vue'
import '../../assets/css/tokens/settings.css'

/**
 * One source-specific setting with the inherited/override rail kept as the only
 * visual accent. Every mutation is controlled by the parent: stories show the
 * last confirmed value while local edits are prepared or a write fails.
 */
const meta = {
  title: 'Settings/SourceOverrideRow',
  component: SourceOverrideRow,
  parameters: { layout: 'padded' },
  decorators: [
    () => ({ template: '<div style="width:min(100%,760px)"><story /></div>' }),
  ],
  args: {
    settingKey: 'downloadConcurrency',
    name: 'Chapter concurrency',
    hint: 'Maximum chapter downloads this source may run at once',
    control: 'number',
    modelValue: 5,
    globalValue: 5,
    inherited: true,
    saving: false,
    error: null,
  },
} satisfies Meta<typeof SourceOverrideRow>

export default meta
type Story = StoryObj<typeof meta>

/** Uses the global value and keeps the clear action unavailable. */
export const Inherited: Story = {}

/** A source-specific value lights the violet status rail. */
export const Overridden: Story = {
  args: {
    modelValue: 1,
    inherited: false,
  },
}

/** Only this row is disabled while its write is in flight. */
export const Saving: Story = {
  args: {
    modelValue: 1,
    inherited: false,
    saving: true,
  },
}

/** The failed edit is explained without replacing the confirmed value. */
export const RowLocalError: Story = {
  args: {
    settingKey: 'imageRequestDelay',
    name: 'Image request delay',
    hint: 'Delay between image requests; 0s disables pacing',
    control: 'text',
    modelValue: '1250ms',
    globalValue: '500ms',
    inherited: false,
    error: 'Image delay must use whole milliseconds.',
  },
}

/** Keyboard traversal lands on a visibly focused, genuinely labelled field. */
export const KeyboardFocus: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.tab()
    await expect(canvas.getByLabelText('Chapter concurrency override')).toHaveFocus()
  },
}

/** Long endpoint-like values wrap inside the row instead of widening the page. */
export const LongValue: Story = {
  parameters: { viewport: { defaultViewport: 'mobile1' } },
  args: {
    settingKey: 'bypassBinding',
    name: 'Bypass route',
    hint: 'Use a source-specific bypass endpoint for protected requests',
    control: 'select',
    modelValue: 'flaresolverr-vpn-archive-route-with-a-deliberately-long-display-name',
    globalValue: 'Primary shared bypass endpoint with automatic fallback',
    inherited: false,
    options: [
      { value: 'global', label: 'Primary shared bypass endpoint with automatic fallback' },
      { value: 'flaresolverr-vpn-archive-route-with-a-deliberately-long-display-name', label: 'Archive VPN FlareSolverr route for alternate English releases' },
    ],
  },
  play: async ({ canvasElement }) => {
    const row = canvasElement.querySelector<HTMLElement>('.source-override')
    await expect(row).not.toBeNull()
    await expect(row!.scrollWidth).toBeLessThanOrEqual(row!.clientWidth)
  },
}
