import type { StorybookConfig } from '@storybook-vue/nuxt'

const config: StorybookConfig = {
  stories: ['../app/**/*.stories.@(ts|js)'],
  addons: [],
  framework: {
    name: '@storybook-vue/nuxt',
    options: {},
  },
}

export default config
