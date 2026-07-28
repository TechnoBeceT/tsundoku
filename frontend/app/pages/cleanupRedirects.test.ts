/**
 * Legacy cleanup routes — the redirect guard.
 *
 * `/fractionals` and `/sourceless` were standalone pages before the 3-tab Cleanup
 * console existed. They must keep working as bookmarks, so each is now a page
 * whose ONLY job is a route-level redirect to its tab on `/cleanup`.
 *
 * A route redirect is a BUILD-TIME routing property declared in `definePageMeta`,
 * which vue-router applies before the component ever renders — mounting the
 * component in isolation cannot observe it (the same blind spot pagesStructure
 * covers for nested routes). So this guard reads the page sources and pins three
 * things per legacy route: it declares a redirect, that redirect targets
 * `/cleanup`, and it carries the correct `tab` query — because a redirect to
 * `/cleanup` with the WRONG tab (or none) is the silent failure here: the link
 * still "works", it just lands the owner on the wrong surface.
 *
 * Non-vacuous: point either redirect at a bare '/cleanup' and its tab assertion
 * fails; delete a redirect and the first assertion fails.
 */
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'

const PAGES_DIR = resolve(__dirname)

const LEGACY_ROUTES: { page: string, tab: string }[] = [
  { page: 'fractionals.vue', tab: 'fractionals' },
  { page: 'sourceless.vue', tab: 'sourceless' },
]

describe('legacy cleanup routes redirect into the console', () => {
  it.each(LEGACY_ROUTES)('$page redirects to /cleanup?tab=$tab', ({ page, tab }) => {
    const src = readFileSync(join(PAGES_DIR, page), 'utf8')

    expect(src, `${page} must declare a route redirect in definePageMeta`).toContain('redirect:')
    expect(src, `${page} must redirect to the Cleanup console`).toContain(`path: '/cleanup'`)
    expect(src, `${page} must carry its own tab in the redirect query`).toContain(`tab: '${tab}'`)
  })

  it.each(LEGACY_ROUTES)('$page no longer renders a screen of its own', ({ page }) => {
    const src = readFileSync(join(PAGES_DIR, page), 'utf8')

    // The screen components are REUSED as tab bodies by the console; a legacy page
    // that still mounted one would be a second, drifting copy of that surface.
    expect(src).not.toContain('useFractionals(')
    expect(src).not.toContain('useSourceless(')
  })
})
