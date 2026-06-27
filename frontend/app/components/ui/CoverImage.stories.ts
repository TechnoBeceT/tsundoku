import type { Meta, StoryObj } from '@storybook/vue3'
import CoverImage from './CoverImage.vue'

// A small inline SVG data-URI so the with-image story needs no network/asset.
const SAMPLE = `data:image/svg+xml;utf8,${encodeURIComponent(
  '<svg xmlns="http://www.w3.org/2000/svg" width="200" height="280"><rect width="200" height="280" fill="#6d28d9"/><circle cx="100" cy="120" r="46" fill="#a78bfa"/></svg>',
)}`

/**
 * Stories for CoverImage. The meaningful states are covered: a real image, the
 * brand placeholder (no src), the initial-letter placeholder, an aspect override,
 * and the small clickable thumbnail (shrunken BrandMark + button root). Each is
 * rendered inside a fixed-width frame so the portrait box is visible. Flip the
 * theme toolbar to confirm the placeholder tile holds in both themes.
 */
const meta = {
  title: 'UI/CoverImage',
  component: CoverImage,
  argTypes: {
    placeholder: { control: { type: 'inline-radio' }, options: ['brand', 'initial'] },
    markSize: { control: { type: 'number' } },
    radius: { control: { type: 'text' } },
    clickable: { control: { type: 'boolean' } },
  },
  args: { alt: 'Sample Series', placeholder: 'brand', aspect: '0.72' },
  render: (args) => ({
    components: { CoverImage },
    setup: () => ({ args }),
    template: '<div style="width:180px"><CoverImage v-bind="args" /></div>',
  }),
} satisfies Meta<typeof CoverImage>

export default meta
type Story = StoryObj<typeof meta>

/** A loaded cover image. */
export const WithImage: Story = {
  args: { src: SAMPLE, alt: 'Loaded cover' },
}

/** No src → the white inverse BrandMark placeholder. */
export const BrandPlaceholder: Story = {
  args: { src: '', placeholder: 'brand' },
}

/** No src → a big faint initial letter (from `initial`, else `alt`). */
export const InitialPlaceholder: Story = {
  args: { src: '', placeholder: 'initial', initial: 'T', alt: 'Tower of God' },
}

/** A wider aspect override (landscape-ish). */
export const CustomAspect: Story = {
  args: { src: '', placeholder: 'initial', initial: 'W', alt: 'Wide', aspect: '1.4' },
}

/** A small clickable thumbnail — shrunken BrandMark, button root, tighter radius. */
export const ClickableThumbnail: Story = {
  args: { src: '', placeholder: 'brand', clickable: true, markSize: 22, radius: 'var(--radius-md)' },
  render: (args) => ({
    components: { CoverImage },
    setup: () => ({ args }),
    template: '<div style="width:60px"><CoverImage v-bind="args" @click="() => {}" /></div>',
  }),
}
