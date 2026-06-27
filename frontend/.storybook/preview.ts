import type { Preview } from '@storybook/vue3'
// Global design tokens so every story renders themed (also applied via Nuxt).
import '../app/assets/css/index.css'

const preview: Preview = {
  // Light/dark toolbar. The app themes via `data-theme="dark|light"` on <html>
  // (see nuxt.config colorMode), so the decorator below sets that SAME attribute
  // — NOT a `.dark` class — keeping Storybook faithful to the running app.
  globalTypes: {
    theme: {
      description: 'Theme',
      defaultValue: 'dark',
      toolbar: {
        title: 'Theme',
        icon: 'circlehollow',
        items: [
          { value: 'dark', title: 'Dark' },
          { value: 'light', title: 'Light' },
        ],
        dynamicTitle: true,
      },
    },
  },
  decorators: [
    (story, context) => {
      if (typeof document !== 'undefined') {
        const theme = context.globals.theme === 'light' ? 'light' : 'dark'
        document.documentElement.setAttribute('data-theme', theme)
        document.documentElement.style.background = 'var(--bg)'
      }
      const full = context.parameters?.layout === 'fullscreen'
      return { components: { story }, template: full ? '<story />' : '<div style="padding:24px"><story /></div>' }
    },
  ],
  parameters: {
    // No hardcoded `backgrounds` block: the decorator paints `var(--bg)` from the
    // tokens, so the canvas follows the theme toolbar and can never drift from the
    // `--bg` token the running app uses.
    controls: { matchers: { color: /(background|color)$/i, date: /Date$/i } },
  },
}

export default preview
