// Lets plain TypeScript (e.g. tooling that checks *.stories.ts outside the
// Vue language server) resolve `*.vue` imports. The Vue TS plugin handles real
// type inference; this shim only stops "Cannot find module './X.vue'" errors.
declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>
  export default component
}
