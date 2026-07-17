/**
 * DisclosurePanel.logic — pins the controlled/uncontrolled resolution and the
 * count-badge rule.
 *
 * Non-vacuous: each case asserts a DIFFERENT branch's outcome, including the
 * two traps the kernel exists to prevent — a `null` `open` must NOT read as
 * "controlled and closed", and a count of `0` must still render a badge.
 */
import { describe, expect, it } from 'vitest'
import { countLabel, resolveOpen } from './DisclosurePanel.logic'

describe('resolveOpen', () => {
  it('is always open when the panel is not collapsible', () => {
    expect(resolveOpen(false, false, false)).toBe(true)
  })

  it('uses local state when the host does not control `open`', () => {
    expect(resolveOpen(true, undefined, true)).toBe(true)
    expect(resolveOpen(true, undefined, false)).toBe(false)
  })

  it('treats a null `open` as uncontrolled rather than closed', () => {
    expect(resolveOpen(true, null, true)).toBe(true)
  })

  it('uses the host value when `open` is controlled', () => {
    expect(resolveOpen(true, false, true)).toBe(false)
    expect(resolveOpen(true, true, false)).toBe(true)
  })
})

describe('countLabel', () => {
  it('renders a zero count (an empty list still says "0")', () => {
    expect(countLabel(0)).toBe('0')
  })

  it('renders numbers and strings', () => {
    expect(countLabel(512)).toBe('512')
    expect(countLabel('3 of 40')).toBe('3 of 40')
  })

  it('hides the badge when there is no count', () => {
    expect(countLabel(null)).toBe('')
    expect(countLabel(undefined)).toBe('')
  })
})
