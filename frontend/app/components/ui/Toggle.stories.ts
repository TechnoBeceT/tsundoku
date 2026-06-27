import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import Toggle from './Toggle.vue'

/**
 * Stories for the Toggle switch. The interactive stories wire a local ref to
 * v-model so the knob slides on click; the disabled stories show the dimmed
 * on/off looks. Flip the Storybook theme toolbar to confirm the track + knob
 * read in both themes.
 */
const meta = {
  title: 'UI/Toggle',
  component: Toggle,
  argTypes: {
    modelValue: { control: 'boolean' },
    disabled: { control: 'boolean' },
  },
  args: { modelValue: false, disabled: false, ariaLabel: 'Monitored' },
} satisfies Meta<typeof Toggle>

export default meta
type Story = StoryObj<typeof meta>

// Shared interactive template: binds a local ref so the switch actually toggles.
const interactive = (initial: boolean, disabled = false) => () => ({
  components: { Toggle },
  setup: () => ({ on: ref(initial), disabled }),
  template: '<Toggle v-model="on" :disabled="disabled" aria-label="Monitored" />',
})

/** Off, interactive. */
export const Off: Story = { render: interactive(false) }

/** On, interactive. */
export const On: Story = { render: interactive(true) }

/** Disabled in the off position. */
export const DisabledOff: Story = { render: interactive(false, true) }

/** Disabled in the on position. */
export const DisabledOn: Story = { render: interactive(true, true) }
