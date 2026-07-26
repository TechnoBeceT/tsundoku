/**
 * dedupSweepSummary — the parsing/formatting kernel behind the Settings
 * library-wide dedup dialog. The sweep runs detached, so this SSE payload is the
 * ONLY report of what it did; these tests pin that the three counts stay
 * individually meaningful on the way to the screen, and that an untrusted
 * payload can never render "undefined" at the owner.
 */
import { describe, it, expect } from 'vitest'
import { readDedupSweepEvent, busyNotice } from './dedupSweepSummary'

describe('readDedupSweepEvent', () => {
  it('reports what actually changed on a clean sweep', () => {
    const s = readDedupSweepEvent({ seriesProcessed: 42, merged: 3, skipped: 0, busy: 0 })
    expect(s.message).toContain('merged 3 duplicate sources')
    expect(s.message).toContain('42 series')
    expect(s.error).toBeNull()
    expect(s.busy).toBe(0)
  })

  it('says so plainly when there was nothing to merge', () => {
    const s = readDedupSweepEvent({ seriesProcessed: 42, merged: 0, skipped: 0, busy: 0 })
    expect(s.message).toContain('no duplicate sources found')
  })

  it('keeps the empty-feed skips in the summary and the busy skips OUT of it', () => {
    const s = readDedupSweepEvent({ seriesProcessed: 10, merged: 1, skipped: 2, busy: 4 })
    // `skipped` = pairs waiting on a source refresh — part of the summary.
    expect(s.message).toContain('2 left alone')
    // `busy` = series to re-run — its own number, never folded into the sentence.
    expect(s.message).not.toContain('4')
    expect(s.busy).toBe(4)
  })

  it('surfaces the backend failure sentence instead of a summary', () => {
    const s = readDedupSweepEvent({
      seriesProcessed: 5,
      merged: 1,
      skipped: 0,
      busy: 2,
      error: 'the clean-up ran out of time before finishing — run it again to continue',
    })
    expect(s.message).toBeNull()
    expect(s.error).toContain('ran out of time')
    // A failed sweep still reports what it had to skip.
    expect(s.busy).toBe(2)
  })

  it('singularises one series and one source', () => {
    const s = readDedupSweepEvent({ seriesProcessed: 1, merged: 1, skipped: 0, busy: 0 })
    expect(s.message).toContain('merged 1 duplicate source across 1 series')
  })

  it('degrades a junk payload to zeros rather than rendering undefined', () => {
    for (const junk of [null, undefined, 'nope', 7, {}, { merged: 'lots', busy: null }]) {
      const s = readDedupSweepEvent(junk)
      expect(s.busy).toBe(0)
      expect(s.message).not.toContain('undefined')
      expect(s.message).not.toContain('NaN')
    }
  })
})

describe('busyNotice', () => {
  it('is silent when nothing was busy', () => {
    expect(busyNotice(0)).toBeNull()
    expect(busyNotice(-1)).toBeNull()
  })

  it('tells the owner the count AND what to do about it', () => {
    const line = busyNotice(3)!
    expect(line).toContain('3 series were busy and were skipped')
    expect(line).toContain('run the clean-up again to catch them')
  })

  it('reads correctly for exactly one', () => {
    const line = busyNotice(1)!
    expect(line).toContain('1 series was busy and was skipped')
    expect(line).toContain('catch it')
  })
})
