import type { Meta, StoryObj } from '@storybook/vue3'
import IconButton from './IconButton.vue'

// A reusable pencil-edit icon for the default slot in these stories.
const editIcon =
  '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>'
const trashIcon =
  '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6"/></svg>'

/**
 * Stories for the square IconButton. Shows the default + danger variants, both
 * sizes, and the disabled state. Flip the Storybook theme to confirm both tones.
 */
const meta = {
  title: 'UI/IconButton',
  component: IconButton,
  argTypes: {
    variant: { control: { type: 'inline-radio' }, options: ['default', 'danger'] },
    size: { control: { type: 'inline-radio' }, options: ['xs', 'sm', 'md'] },
    disabled: { control: 'boolean' },
  },
  args: { variant: 'default', size: 'md', disabled: false, ariaLabel: 'Edit' },
  render: (args) => ({
    components: { IconButton },
    setup: () => ({ args }),
    template: `<IconButton v-bind="args">${editIcon}</IconButton>`,
  }),
} satisfies Meta<typeof IconButton>

export default meta
type Story = StoryObj<typeof meta>

/** Default neutral icon button. */
export const Default: Story = {}

/** The destructive (danger) treatment. */
export const Danger: Story = {
  args: { variant: 'danger', ariaLabel: 'Delete' },
  render: (args) => ({
    components: { IconButton },
    setup: () => ({ args }),
    template: `<IconButton v-bind="args">${trashIcon}</IconButton>`,
  }),
}

/** Both variants across the size ladder (`xs` is the inline row action). */
export const Matrix: Story = {
  render: () => ({
    components: { IconButton },
    template:
      '<div style="display:flex;gap:12px;align-items:center">' +
      `<IconButton variant="default" size="xs" aria-label="Edit">${editIcon}</IconButton>` +
      `<IconButton variant="default" size="sm" aria-label="Edit">${editIcon}</IconButton>` +
      `<IconButton variant="default" size="md" aria-label="Edit">${editIcon}</IconButton>` +
      `<IconButton variant="danger" size="xs" aria-label="Delete">${trashIcon}</IconButton>` +
      `<IconButton variant="danger" size="sm" aria-label="Delete">${trashIcon}</IconButton>` +
      `<IconButton variant="danger" size="md" aria-label="Delete">${trashIcon}</IconButton>` +
      '</div>',
  }),
}

/** Disabled state. */
export const Disabled: Story = {
  args: { disabled: true },
}
