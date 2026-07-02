import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, within } from 'storybook/test'
import SourcePreferenceControl from './SourcePreferenceControl.vue'
import { editPref, listPref, multiPref, switchPref } from '../../fixtures/preferences'
import '../../assets/css/tokens/settings.css'

/**
 * Stories for one preference control — one story per union variant, so each
 * rendered control (Toggle / Select / multi-select / TextField) is visually
 * verified. Flip the theme toolbar for dark/light.
 */
const meta = {
  title: 'Settings/SourcePreferenceControl',
  component: SourcePreferenceControl,
  parameters: { layout: 'padded' },
  args: { sourceId: 'src-en', busy: false },
} satisfies Meta<typeof SourcePreferenceControl>

export default meta
type Story = StoryObj<typeof meta>

/** Switch / CheckBox → a Toggle that commits on flip. */
export const Switch: Story = {
  args: { preference: switchPref },
  play: async ({ canvasElement }) => {
    // The Toggle renders as a real role="switch", reflecting the ON currentValue.
    const toggle = within(canvasElement).getByRole('switch')
    await expect(toggle).toHaveAttribute('data-state', 'checked')
  },
}

/** List → a SelectField that commits on selection. */
export const List: Story = {
  args: { preference: listPref },
}

/** MultiSelectList → a checkbox group that commits on toggle. */
export const MultiSelect: Story = {
  args: { preference: multiPref },
}

/** EditText → a TextField that commits on Enter/blur. */
export const EditText: Story = {
  args: { preference: editPref },
}

/** §16 busy — the control disables and a spinner shows. */
export const Busy: Story = {
  args: { preference: switchPref, busy: true },
}
