import type { Meta, StoryObj } from '@storybook/vue3'
import SourceThroughputControl from './SourceThroughputControl.vue'
import { inheritedThroughputPolicy, overriddenThroughputPolicy } from '../../fixtures/settings'
import '../../assets/css/tokens/settings.css'

const meta = { title: 'Settings/SourceThroughputControl', component: SourceThroughputControl, parameters: { layout: 'padded' }, args: { policy: inheritedThroughputPolicy, sourceName: 'ComicK Fanmade' } } satisfies Meta<typeof SourceThroughputControl>
export default meta
type Story = StoryObj<typeof meta>
export const Inherited: Story = {}
export const Overridden: Story = { args: { policy: overriddenThroughputPolicy, sourceName: 'Comic Asura' } }
export const Saving: Story = { args: { policy: overriddenThroughputPolicy, saving: true } }
export const Error: Story = { args: { policy: overriddenThroughputPolicy, error: 'Image delay must use whole milliseconds.' } }
