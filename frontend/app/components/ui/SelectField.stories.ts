import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import SelectField from './SelectField.vue'

const categoryOptions = [
  { value: 'manga', label: 'Manga' },
  { value: 'manhwa', label: 'Manhwa' },
  { value: 'manhua', label: 'Manhua' },
  { value: 'comic', label: 'Comic' },
  { value: 'other', label: 'Other' },
]

/**
 * Stories for the token-styled native SelectField. Covers the default picker and
 * the disabled state. Flip the theme to confirm the custom chevron + token
 * surface in both modes.
 */
const meta = {
  title: 'UI/SelectField',
  component: SelectField,
  argTypes: {
    disabled: { control: 'boolean' },
  },
  args: { disabled: false, ariaLabel: 'Category' },
  render: (args) => ({
    components: { SelectField },
    setup: () => {
      const value = ref('manhwa')
      return { args, value, categoryOptions }
    },
    template: '<div style="max-width:240px"><SelectField v-bind="args" v-model="value" :options="categoryOptions" /></div>',
  }),
} satisfies Meta<typeof SelectField>

export default meta
type Story = StoryObj<typeof meta>

/** Default select. */
export const Default: Story = {}

/** Disabled select. */
export const Disabled: Story = {
  args: { disabled: true },
}
