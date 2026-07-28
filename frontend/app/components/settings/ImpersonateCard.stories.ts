import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import ImpersonateCard from './ImpersonateCard.vue'
import { impersonateConfig, impersonateSources } from '../../fixtures/settings'
import type { ImpersonateConfig, SourceOption } from '../screens/settings.types'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the toggle-gated impersonate-gateway card. The wrapper holds a
 * live model so the enable toggle reveals/hides the URL field + the per-source
 * opt-in list, and so ticking a source is interactive. Flip the theme toolbar
 * for each.
 */
const meta = {
  title: 'Settings/ImpersonateCard',
  component: ImpersonateCard,
  parameters: { layout: 'padded' },
  // modelValue is a required prop; each story renders its own live-model wrapper,
  // so this default only satisfies the CSF3 story typing.
  args: { modelValue: impersonateConfig, sources: impersonateSources },
} satisfies Meta<typeof ImpersonateCard>

export default meta
type Story = StoryObj<typeof meta>

// A live-model wrapper so the toggle, URL field and source ticks are interactive.
const withModel = (seed: ImpersonateConfig, sources: SourceOption[] = impersonateSources) => ({
  components: { ImpersonateCard },
  setup() {
    const model = ref<ImpersonateConfig>({ ...seed, sourceIds: [...seed.sourceIds] })
    return { model, sources }
  },
  template: `<ImpersonateCard v-model="model" :sources="sources" />`,
})

/** Enabled with one source opted in — the URL field, hint and picker all show. */
export const On: Story = {
  render: () => withModel(impersonateConfig),
}

/** Disabled — only the header + toggle show. */
export const Off: Story = {
  render: () => withModel({ ...impersonateConfig, enabled: false }),
}

/**
 * Enabled with NOTHING opted in — the safe default state. The count reads
 * "0 selected" and no source uses the proxy, so every source keeps its own image
 * processing (GAP-131).
 */
export const NoSourcesSelected: Story = {
  render: () => withModel({ ...impersonateConfig, sourceIds: [] }),
}

/**
 * The engine could not list its sources (empty list). The card says so rather
 * than implying the saved selection was lost.
 */
export const NoSourcesAvailable: Story = {
  render: () => withModel(impersonateConfig, []),
}
