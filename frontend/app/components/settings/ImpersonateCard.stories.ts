import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import ImpersonateCard from './ImpersonateCard.vue'
import { impersonateConfig } from '../../fixtures/settings'
import type { ImpersonateConfig } from '../screens/settings.types'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the toggle-gated impersonate-gateway card. The wrapper holds a
 * live model so the enable toggle reveals/hides the URL field. Flip the theme
 * toolbar for both.
 */
const meta = {
  title: 'Settings/ImpersonateCard',
  component: ImpersonateCard,
  parameters: { layout: 'padded' },
  // modelValue is a required prop; each story renders its own live-model wrapper,
  // so this default only satisfies the CSF3 story typing.
  args: { modelValue: impersonateConfig },
} satisfies Meta<typeof ImpersonateCard>

export default meta
type Story = StoryObj<typeof meta>

// A live-model wrapper so the toggle + URL field are interactive in the story.
const withModel = (seed: ImpersonateConfig) => ({
  components: { ImpersonateCard },
  setup() {
    const model = ref<ImpersonateConfig>({ ...seed })
    return { model }
  },
  template: `<ImpersonateCard v-model="model" />`,
})

/** Enabled — the gateway URL field + hint show (seed config). */
export const On: Story = {
  render: () => withModel(impersonateConfig),
}

/** Disabled — only the header + toggle show. */
export const Off: Story = {
  render: () => withModel({ ...impersonateConfig, enabled: false }),
}
