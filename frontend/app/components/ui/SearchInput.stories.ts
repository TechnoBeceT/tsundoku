import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import SearchInput from './SearchInput.vue'

/**
 * Stories for the SearchInput. Covers the empty state, a pre-filled value (so the
 * clear × shows), and the non-clearable variant. Flip the theme to confirm the
 * leading icon + focus ring read in both modes.
 */
const meta = {
  title: 'UI/SearchInput',
  component: SearchInput,
  argTypes: {
    placeholder: { control: 'text' },
    clearable: { control: 'boolean' },
  },
  // modelValue is a required prop; every story overrides it with a local ref via
  // v-model, so this default only satisfies the CSF3 story typing.
  args: { modelValue: '', placeholder: 'Search a title across sources…', clearable: true },
  render: (args) => ({
    components: { SearchInput },
    setup: () => {
      const value = ref('')
      return { args, value }
    },
    template: '<div style="max-width:340px"><SearchInput v-bind="args" v-model="value" /></div>',
  }),
} satisfies Meta<typeof SearchInput>

export default meta
type Story = StoryObj<typeof meta>

/** Empty search field. */
export const Empty: Story = {}

/** Pre-filled — the clear (×) button is visible. */
export const Filled: Story = {
  render: (args) => ({
    components: { SearchInput },
    setup: () => {
      const value = ref('Solo Leveling')
      return { args, value }
    },
    template: '<div style="max-width:340px"><SearchInput v-bind="args" v-model="value" /></div>',
  }),
}

/** Non-clearable variant (no × even when filled). */
export const NotClearable: Story = {
  args: { clearable: false },
  render: (args) => ({
    components: { SearchInput },
    setup: () => {
      const value = ref('Omniscient Reader')
      return { args, value }
    },
    template: '<div style="max-width:340px"><SearchInput v-bind="args" v-model="value" /></div>',
  }),
}
