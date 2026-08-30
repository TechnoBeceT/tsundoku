import type { Meta, StoryObj } from '@storybook/vue3'
import NetworkPane from './NetworkPane.vue'
import { networkEndpoints } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for reusable endpoint CRUD in the Download engine Routing section.
 * Per-source assignment now lives only in the canonical Source exceptions panel.
 */
const meta = {
  title: 'Settings/NetworkPane',
  component: NetworkPane,
  parameters: { layout: 'padded' },
  args: {
    endpoints: networkEndpoints,
  },
} satisfies Meta<typeof NetworkPane>

export default meta
type Story = StoryObj<typeof meta>

/** Populated — reusable SOCKS and FlareSolverr endpoints remain editable. */
export const Populated: Story = {}

/** Empty — no endpoints defined and no sources installed yet. */
export const Empty: Story = {
  args: { endpoints: [] },
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
  args: { endpoints: [], endpointsPending: true },
}
