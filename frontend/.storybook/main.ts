import type { StorybookConfig } from '@storybook-vue/nuxt'

const config: StorybookConfig = {
  stories: ['../app/**/*.stories.@(ts|js)'],
  // Serve `public/` at the site root (mirrors the Nuxt app + the Go SPA
  // static middleware) — needed so TrackerIcon's `/tracker/<name>.png` brand
  // logos resolve inside the Storybook preview iframe, not just the real app.
  staticDirs: ['../public'],
  addons: [],
  framework: {
    name: '@storybook-vue/nuxt',
    options: {},
  },
}

export default config
