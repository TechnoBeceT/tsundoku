/**
 * fileSize — byte-count formatting for the Cleanup console's reclaimable totals.
 *
 * Pins the unit ladder, the 1024 base, the one-decimal rule above KB, and the
 * degenerate inputs (0, negatives, non-finite) that a wire value could carry.
 *
 * Non-vacuous: switch the base to 1000 and the KB/MB/GB assertions all fail; drop
 * the whole-number rule for bytes and the "0 B"/"512 B" assertions fail.
 */
import { describe, it, expect } from 'vitest'
import { formatBytes } from './fileSize'

describe('formatBytes', () => {
  it('renders a zero total as 0 B', () => {
    expect(formatBytes(0)).toBe('0 B')
  })

  it('renders bytes with no decimal', () => {
    expect(formatBytes(512)).toBe('512 B')
  })

  it('steps up to KB at 1024 (binary base, not 1000)', () => {
    expect(formatBytes(1024)).toBe('1.0 KB')
    expect(formatBytes(1000)).toBe('1000 B')
  })

  it('renders MB and GB with one decimal', () => {
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB')
    expect(formatBytes(Math.round(2.97 * 1024 * 1024 * 1024))).toBe('3.0 GB')
  })

  it('stops at TB rather than inventing a larger unit', () => {
    expect(formatBytes(3 * 1024 ** 4)).toBe('3.0 TB')
    expect(formatBytes(4096 * 1024 ** 4)).toBe('4096.0 TB')
  })

  it('treats a negative or non-finite total as 0 B rather than rendering nonsense', () => {
    expect(formatBytes(-1)).toBe('0 B')
    expect(formatBytes(Number.NaN)).toBe('0 B')
    expect(formatBytes(Number.POSITIVE_INFINITY)).toBe('0 B')
  })
})
