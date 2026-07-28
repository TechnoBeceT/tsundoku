/**
 * cleanupTabs — deep-link + sessionStorage tab resolution for the /cleanup console.
 *
 * Pins the resolution precedence the Cleanup console relies on (identical in shape
 * to healthTabs, which is the pattern this mirrors):
 *   1. a valid `?tab=` query wins over everything — the two REDIRECTED legacy
 *      routes (/fractionals, /sourceless) land via exactly that query, so this
 *      precedence is what keeps old bookmarks working,
 *   2. else the persisted session tab,
 *   3. else the `fractionals` default,
 *   with unknown values in either input ignored.
 *
 * Non-vacuous: let an unknown query win and the fallback assertions fail; drop the
 * query-over-stored precedence and the redirect assertions return the stored tab.
 */
import { describe, it, expect } from 'vitest'
import { resolveInitialCleanupTab, CLEANUP_TABS, CLEANUP_TAB_SESSION_KEY } from './cleanupTabs'

describe('resolveInitialCleanupTab', () => {
  it('defaults to fractionals when nothing is set', () => {
    expect(resolveInitialCleanupTab(null, null)).toBe('fractionals')
  })

  it('honours a ?tab=fractionals deep-link', () => {
    expect(resolveInitialCleanupTab('fractionals', null)).toBe('fractionals')
  })

  it('honours a ?tab=sourceless deep-link', () => {
    expect(resolveInitialCleanupTab('sourceless', null)).toBe('sourceless')
  })

  it('honours a ?tab=duplicates deep-link', () => {
    expect(resolveInitialCleanupTab('duplicates', null)).toBe('duplicates')
  })

  it('falls back to the stored tab when the query is absent', () => {
    expect(resolveInitialCleanupTab(null, 'duplicates')).toBe('duplicates')
  })

  it('ignores an unknown query and falls back to the stored tab', () => {
    expect(resolveInitialCleanupTab('bogus', 'sourceless')).toBe('sourceless')
  })

  it('ignores an unknown stored value and uses the default', () => {
    expect(resolveInitialCleanupTab(null, 'garbage')).toBe('fractionals')
  })

  it('lets a redirected /sourceless bookmark win over a stored duplicates tab', () => {
    // pages/sourceless.vue redirects to /cleanup?tab=sourceless. The forced query
    // MUST beat sessionStorage, else an old bookmark would drop the owner on
    // whichever tab they happened to use last — the link would look broken.
    expect(resolveInitialCleanupTab('sourceless', 'duplicates')).toBe('sourceless')
  })
})

describe('cleanupTabs constants', () => {
  it('exposes the three tabs in order (fractionals first)', () => {
    expect(CLEANUP_TABS.map(t => t.key)).toEqual(['fractionals', 'sourceless', 'duplicates'])
  })

  it('uses a stable sessionStorage key', () => {
    expect(CLEANUP_TAB_SESSION_KEY).toBe('tsundoku.cleanup.tab')
  })
})
