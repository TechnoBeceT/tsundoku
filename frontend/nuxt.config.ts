// nuxt.config.ts — Nuxt 4 configuration for the Tsundoku frontend.
//
// ssr: false — the app is served as a static SPA from the Go backend
// (same-origin, no CORS, per QCAT-020). The built output in .output/public/
// is served directly by the Echo static-file middleware in Task 9.
export default defineNuxtConfig({
  compatibilityDate: "2025-06-22",

  // SPA mode: disable SSR so the build produces a static bundle the Go
  // backend can serve directly.
  ssr: false,

  future: {
    // Opt into Nuxt 4 directory conventions.
    compatibilityVersion: 4,
  },
})
