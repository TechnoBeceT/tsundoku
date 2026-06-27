// nuxt.config.ts — Nuxt 4 configuration for the Tsundoku frontend.
//
// ssr: false — the app is served as a static SPA from the Go backend
// (same-origin, no CORS, per QCAT-020). The built output in .output/public/
// is served directly by the Echo static-file middleware.

// `process.env.STORYBOOK` is set by the `storybook`/`build-storybook` scripts
// (see package.json) and is the only Node global this config reads. Nuxt
// type-checks config files with no @types/node, so `process` is declared
// locally rather than pulling Node's full ambient types into the build config.
declare const process: { env: Record<string, string | undefined> }

export default defineNuxtConfig({
  compatibilityDate: '2025-06-22',
  devtools: { enabled: true },

  // SPA mode: disable SSR so the build produces a static bundle the Go backend
  // can serve directly. Auth is a single-owner Bearer token over the JSON API
  // (QCAT-020) — there is no SSR data layer to maintain.
  ssr: false,

  future: {
    // Opt into Nuxt 4 directory conventions (app/ root, etc.).
    compatibilityVersion: 4,
  },

  modules: [
    '@nuxt/icon',
    '@nuxt/eslint',
    // @nuxt/fonts and @nuxtjs/color-mode break under @storybook-vue/nuxt's
    // server/mount, so they are excluded when STORYBOOK=1. The app uses both;
    // in Storybook, fonts load via .storybook/preview-head.html and the theme
    // is toggled by a decorator that sets `data-theme` on <html>.
    ...(process.env.STORYBOOK ? [] : ['@nuxt/fonts', '@nuxtjs/color-mode']),
  ],

  // Inline-SVG icon mode: @nuxt/icon renders icons as inline <svg> from the
  // bundled @iconify-json/lucide set (no runtime network fetch). MANDATORY —
  // the default API mode silently fails offline / same-origin.
  icon: {
    mode: 'svg',
  },

  // ESLint flat-config generation. `@nuxt/eslint` produces a project-aware base
  // config at .nuxt/eslint.config.mjs (Vue SFC parser + Nuxt auto-import globals
  // so `ref`, `computed`, `defineProps` etc. don't trip no-undef). Our root
  // eslint.config.mjs extends it and layers typescript-eslint's type-checked
  // rules on top. `stylistic: false` — formatting is not linted here.
  eslint: {
    config: {
      stylistic: false,
    },
  },

  // Global design-system entry: tokens + base + utilities (the ONLY CSS entry).
  // Component-specific styles live in each SFC's <style scoped>, not here.
  css: ['~/assets/css/index.css'],

  // The prototype themes via a `theme` value applied as CSS vars keyed off
  // `data-theme="dark|light"` on <html>, and DEFAULTS TO DARK. `dataValue:
  // 'theme'` makes @nuxtjs/color-mode write that exact attribute; `classSuffix:
  // ''` keeps the class plain. Our token blocks select on `[data-theme=...]`.
  colorMode: {
    dataValue: 'theme',
    classSuffix: '',
    preference: 'dark',
    fallback: 'dark',
  },

  // Self-hosted webfonts via @nuxt/fonts. Weights mirror the prototype's
  // Google Fonts link exactly: Zen Kaku Gothic New (display), Hanken Grotesk
  // (sans/body), JetBrains Mono (mono/labels).
  fonts: {
    families: [
      { name: 'Zen Kaku Gothic New', provider: 'google', weights: [500, 700, 900] },
      { name: 'Hanken Grotesk', provider: 'google', weights: [400, 500, 600, 700, 800] },
      { name: 'JetBrains Mono', provider: 'google', weights: [400, 500, 700] },
    ],
  },

  // Component auto-import — every component registers by its bare PascalCase
  // name (`pathPrefix: false`) regardless of domain folder (`<BrandMark>`,
  // `<BrandLockup>`). Matches the house convention (one plain name per
  // component) and how templates actually reference them. `types.ts` files are
  // shared prop/data types, NOT components — ignore them so two domain folders'
  // `types.ts` don't both register as a "Types" component (a name collision).
  components: [
    { path: '~/components', pathPrefix: false, ignore: ['**/types.ts'] },
  ],

  app: {
    head: {
      title: 'Tsundoku',
      link: [
        { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' },
        { rel: 'icon', type: 'image/x-icon', href: '/favicon.ico' },
        { rel: 'apple-touch-icon', sizes: '180x180', href: '/apple-touch-icon.png' },
        { rel: 'manifest', href: '/manifest.webmanifest' },
      ],
      meta: [
        { name: 'theme-color', content: '#7c3aed' },
      ],
    },
  },
})
