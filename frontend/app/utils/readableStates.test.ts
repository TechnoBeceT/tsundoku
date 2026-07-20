/**
 * readableStates — the readable-chapter-state set is the ONE gate shared by the
 * reader (`useReader`) and the Series-Detail "Read" button (`ChapterRow`). These
 * pin that a CBZ-on-disk state (`downloaded`/`upgrade_available`/`upgrading`) is
 * readable while every other download state is not.
 */
import { describe, it, expect } from 'vitest'
import { READABLE_STATES, isReadableState } from './readableStates'

describe('readableStates', () => {
  it('holds exactly the three on-disk-CBZ states', () => {
    expect([...READABLE_STATES].sort()).toEqual(['downloaded', 'upgrade_available', 'upgrading'])
  })

  it.each(['downloaded', 'upgrade_available', 'upgrading'])('isReadableState(%s) is true', (s) => {
    expect(isReadableState(s)).toBe(true)
  })

  it.each(['wanted', 'downloading', 'failed', 'permanently_failed', 'superseded', 'ignored', ''])(
    'isReadableState(%s) is false',
    (s) => {
      expect(isReadableState(s)).toBe(false)
    },
  )
})
