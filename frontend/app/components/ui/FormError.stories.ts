import type { Meta, StoryObj } from '@storybook/vue3'
import FormError from './FormError.vue'

/**
 * Stories for the FormError inline line. Shows a typical validation message and
 * a longer one (to confirm wrapping). Flip the theme toolbar to confirm the
 * danger tone reads in both themes.
 */
const meta = {
  title: 'UI/FormError',
  component: FormError,
  args: { message: 'Enter a valid URL (https://…)' },
} satisfies Meta<typeof FormError>

export default meta
type Story = StoryObj<typeof meta>

/** A typical short validation message. */
export const Default: Story = {}

/** A longer message, to confirm it wraps cleanly. */
export const LongMessage: Story = {
  args: {
    message: 'That category name is already in use. Pick a different name and try again.',
  },
}
