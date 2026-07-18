import type { Meta, StoryObj } from '@storybook/vue3'
import NetworkPane from './NetworkPane.vue'
import { networkEndpoints, networkSources, sourceBindings } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Network pane (per-source SOCKS/FlareSolverr routing). Flip the
 * Storybook theme toolbar to confirm both dark and light. Covers the populated
 * two-card view, the empty state, the delete-in-use (409) degraded state whose
 * banner lists the referencing sources, the loading skeletons, and a binding
 * error.
 */
const meta = {
  title: 'Settings/NetworkPane',
  component: NetworkPane,
  parameters: { layout: 'padded' },
  args: {
    endpoints: networkEndpoints,
    sources: networkSources,
    bindings: sourceBindings,
  },
} satisfies Meta<typeof NetworkPane>

export default meta
type Story = StoryObj<typeof meta>

/** Populated — two endpoints + three sources (Source C bound to both VPN endpoints). */
export const Populated: Story = {}

/** Empty — no endpoints defined and no sources installed yet. */
export const Empty: Story = {
  args: { endpoints: [], sources: [], bindings: [] },
}

/**
 * §16 degraded: a delete was blocked because the endpoint is still referenced —
 * the dismissible banner names the referencing sources so the owner knows what to
 * unbind first.
 */
export const EndpointInUse: Story = {
  args: {
    endpointAction: {
      busyId: null,
      error: 'endpoint is referenced by sources: 9127482910938471028 — clear their bindings first',
    },
  },
}

/** Loading — both cards render their own skeleton rows while data fetches. */
export const Pending: Story = {
  args: { endpoints: [], sources: [], bindings: [], endpointsPending: true, bindingsPending: true },
}

/** §16: a failed binding update surfaces inline above the assignment table. */
export const BindingError: Story = {
  args: { bindingAction: { busyId: null, error: 'FlareSolverr endpoint not found for this binding.' } },
}
