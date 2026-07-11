/**
 * Page-routing structure guard.
 *
 * Nuxt turns `pages/foo.vue` + a sibling `pages/foo/` directory into a NESTED
 * route: everything under `foo/` becomes a CHILD of `foo`. A child route only
 * renders if its parent component provides a `<NuxtPage />` outlet — otherwise
 * navigating to the child silently renders the PARENT and the child never
 * appears. The URL changes, nothing else does.
 *
 * That is a real bug we shipped: `pages/series/[id].vue` coexisted with
 * `pages/series/[id]/read/[chapterId].vue`, so the reader was a child of the
 * series-detail page, which has no outlet — the Read button "did nothing".
 * Every unit test stayed green, because this is a BUILD-TIME routing property
 * that mounting a component in isolation cannot see.
 *
 * The fix is to make such pages SIBLINGS by moving the parent page into the
 * directory as `index.vue` (`pages/foo/index.vue`), leaving no wrapper
 * component. This test pins that invariant:
 *
 *   a page file may only coexist with a same-named directory if it actually
 *   renders a <NuxtPage /> outlet (i.e. it INTENDS to be a nested layout).
 */
import { describe, expect, it } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'

const PAGES_DIR = resolve(__dirname)

/** Recursively collects every `.vue` page file under `pages/`. */
function vuePagesIn(dir: string): string[] {
  return readdirSync(dir).flatMap((entry) => {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) return vuePagesIn(full)
    return entry.endsWith('.vue') ? [full] : []
  })
}

/**
 * A page file "wraps" a same-named directory only when it renders an outlet.
 * `pages/foo.vue` + `pages/foo/` with no `<NuxtPage />` = children can never render.
 */
function isOrphanedParent(pageFile: string): boolean {
  const asDir = pageFile.replace(/\.vue$/, '')
  let hasChildDir = false
  try {
    hasChildDir = statSync(asDir).isDirectory()
  }
  catch {
    return false // no same-named directory — nothing to nest, always fine
  }
  if (!hasChildDir) return false
  return !readFileSync(pageFile, 'utf8').includes('<NuxtPage')
}

describe('pages/ routing structure', () => {
  it('never nests a route under a parent page that has no <NuxtPage /> outlet', () => {
    const offenders = vuePagesIn(PAGES_DIR)
      .filter(isOrphanedParent)
      .map((f) => f.slice(PAGES_DIR.length + 1))

    expect(
      offenders,
      offenders.length
        ? `These page files have a same-named sibling directory but render no <NuxtPage /> outlet, `
          + `so every route under that directory is an unrenderable child (navigation "does nothing"). `
          + `Move each one to <name>/index.vue to make the routes siblings: ${offenders.join(', ')}`
        : '',
    ).toEqual([])
  })
})
