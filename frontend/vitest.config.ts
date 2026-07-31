import { defineVitestConfig } from '@nuxt/test-utils/config'

export default defineVitestConfig({
  test: {
    environment: 'nuxt',
    // Node's own experimental Web Storage globals must be OFF, or the test
    // environment never gets a working `localStorage`.
    //
    // WHY: the 'nuxt' environment builds a happy-dom window and then copies its
    // properties onto the worker's global. That copy step SKIPS any key the
    // global already owns unless the key sits on vitest's built-in allow-list —
    // and `localStorage` / `sessionStorage` are not on it (only the `Storage`
    // constructor is). Node 22+ defines both as its own globals, so happy-dom's
    // real Storage instances lose the race and the tests see Node's instead.
    // Node's `sessionStorage` happens to work (in-memory), but its
    // `localStorage` is undefined unless the process was started with
    // `--localstorage-file=…` — which is why only the localStorage-backed specs
    // broke, and only on a machine running a recent Node.
    //
    // Turning the flag off removes Node's globals entirely, so the copy step
    // passes both keys through and BOTH storages come from happy-dom: fresh per
    // test file, in-memory, torn down with the environment. It is a no-op on
    // Node versions that never exposed the globals.
    execArgv: ['--no-experimental-webstorage'],
  },
})
