import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import FlareSolverrCard from './FlareSolverrCard.vue'
import { flareSolverrConfig } from '../../fixtures/settings'
import type { FlareSolverrConfig } from '../screens/settings.types'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the toggle-gated FlareSolverr card. The wrapper holds a live model
 * so the enable toggle reveals/hides the fields. Flip the theme toolbar for both.
 */
const meta = {
  title: 'Settings/FlareSolverrCard',
  component: FlareSolverrCard,
  parameters: { layout: 'padded' },
  // modelValue is a required prop; each story renders its own live-model wrapper,
  // so this default only satisfies the CSF3 story typing.
  args: { modelValue: flareSolverrConfig },
} satisfies Meta<typeof FlareSolverrCard>

export default meta
type Story = StoryObj<typeof meta>

// A live-model wrapper so the toggle + fields are interactive in the story.
const withModel = (seed: FlareSolverrConfig) => ({
  components: { FlareSolverrCard },
  setup() {
    const model = ref<FlareSolverrConfig>({
      ...seed,
      timeout: { ...seed.timeout },
      sessionTtl: { ...seed.sessionTtl },
    })
    return { model }
  },
  template: `<FlareSolverrCard v-model="model" />`,
})

/** Enabled — the URL, timeout, session, and fallback controls (seed config). */
export const On: Story = {
  render: () => withModel(flareSolverrConfig),
}

/** Disabled — only the header + toggle show. */
export const Off: Story = {
  render: () => withModel({ ...flareSolverrConfig, enabled: false }),
}
