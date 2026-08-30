import type { Meta, StoryObj } from '@storybook/vue3'
import SourceApplyStatus from './SourceApplyStatus.vue'
import {
  fullyInheritedSourceConfiguration,
  pendingSourceException,
  errorSourceException,
} from '../../fixtures/settings'
import '../../assets/css/tokens/settings.css'

/** Compact engine convergence state for a source configuration header. */
const meta = {
  title: 'Settings/SourceApplyStatus',
  component: SourceApplyStatus,
  parameters: { layout: 'padded' },
  args: {
    runtime: fullyInheritedSourceConfiguration.runtime,
  },
} satisfies Meta<typeof SourceApplyStatus>

export default meta
type Story = StoryObj<typeof meta>

/** Desired and applied revisions match. */
export const Applied: Story = {}

/** A newer desired revision is still converging to the engine. */
export const Pending: Story = {
  args: { runtime: pendingSourceException.runtime },
}

/** Pending remains distinct while the most recent apply diagnosis stays visible. */
export const PendingWithError: Story = {
  args: { runtime: errorSourceException.runtime },
}
