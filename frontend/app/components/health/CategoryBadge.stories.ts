import type { Meta, StoryObj } from '@storybook/vue3'
import CategoryBadge from './CategoryBadge.vue'

/**
 * Stories for the error-category badge — one per taxonomy value plus the
 * null/unknown fallback. Tone encodes severity: rose (blocking), amber
 * (recoverable), grey (benign). Flip the theme toolbar to confirm both.
 */
const meta = {
  title: 'Health/CategoryBadge',
  component: CategoryBadge,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof CategoryBadge>

export default meta
type Story = StoryObj<typeof meta>

export const Captcha: Story = { args: { category: 'captcha' } }
export const RateLimit: Story = { args: { category: 'rate_limit' } }
export const NotFound: Story = { args: { category: 'not_found' } }
export const ServerError: Story = { args: { category: 'server_error' } }
export const Network: Story = { args: { category: 'network' } }
export const Timeout: Story = { args: { category: 'timeout' } }
export const Parse: Story = { args: { category: 'parse' } }
export const NoPages: Story = { args: { category: 'no_pages' } }

/** An absent/unmapped category falls back to the neutral "Unknown" pill. */
export const UnknownFallback: Story = { args: { category: null } }
