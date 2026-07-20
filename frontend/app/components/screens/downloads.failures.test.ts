/**
 * downloads.failures — sub-tab routing for the honest failed set.
 *
 * Pins the fix for the empty-Retryable bug: a DOWNLOADED chapter whose upgrade
 * source is failing (retryable) must route into the Retryable sub-tab even though
 * its chapter state is `downloaded`, and a budget-exhausted one into Terminal.
 *
 * Non-vacuous: route by chapter state alone (the old behaviour) and the first two
 * assertions fail (a downloaded row matches neither `failed` nor
 * `permanently_failed`), leaving Retryable empty — the exact prod bug.
 */
import { describe, it, expect } from 'vitest'
import {
  isFailureRow,
  isRetryableFailure,
  isTerminalFailure,
  failSubTabPredicate,
} from './downloads.failures'
import type { DownloadItem } from './downloads.types'

const base: DownloadItem = {
  chapterId: 'c-1',
  seriesId: 's-1',
  seriesTitle: 'Solo Leveling',
  seriesCategory: 'Manhwa',
  coverUrl: '',
  number: 91,
  name: 'Chapter 91',
  state: 'downloaded',
  provider: 'comix-id',
  providerName: 'Comix',
}

// A downloaded chapter whose UPGRADE source is failing but still has budget.
const downloadedRetryable: DownloadItem = {
  ...base,
  isUpgrade: true,
  upgradeTarget: 'Hive Scans',
  failingProviderName: 'Hive Scans',
  failingAttempts: 3,
  maxRetries: 5,
  retryable: true,
  terminal: false,
}

// The same, but the target burned its whole budget.
const downloadedTerminal: DownloadItem = {
  ...base,
  chapterId: 'c-2',
  isUpgrade: true,
  upgradeTarget: 'Hive Scans',
  failingProviderName: 'Hive Scans',
  failingAttempts: 5,
  maxRetries: 5,
  retryable: false,
  terminal: true,
}

// Classic state-based failures — must keep working.
const stateFailed: DownloadItem = { ...base, chapterId: 'c-3', state: 'failed', providerName: 'MangaDex' }
const statePermanent: DownloadItem = { ...base, chapterId: 'c-4', state: 'permanently_failed', providerName: 'Asura' }

// A plain downloaded chapter with no failing source — NOT a failure.
const cleanDownloaded: DownloadItem = { ...base, chapterId: 'c-5' }

describe('downloads.failures – classification', () => {
  it('treats downloaded broken-upgrade rows AND state-failures as failure rows', () => {
    expect(isFailureRow(downloadedRetryable)).toBe(true)
    expect(isFailureRow(downloadedTerminal)).toBe(true)
    expect(isFailureRow(stateFailed)).toBe(true)
    expect(isFailureRow(statePermanent)).toBe(true)
    // A downloaded chapter with no failing source is not a failure.
    expect(isFailureRow(cleanDownloaded)).toBe(false)
  })

  it('routes a downloaded retryable upgrade-failure to Retryable (the bug fix)', () => {
    expect(isRetryableFailure(downloadedRetryable)).toBe(true)
    expect(isTerminalFailure(downloadedRetryable)).toBe(false)
  })

  it('routes a budget-exhausted upgrade-failure to Terminal, not Retryable', () => {
    expect(isTerminalFailure(downloadedTerminal)).toBe(true)
    expect(isRetryableFailure(downloadedTerminal)).toBe(false)
  })

  it('keeps state-based failures routing by state', () => {
    expect(isRetryableFailure(stateFailed)).toBe(true)
    expect(isTerminalFailure(stateFailed)).toBe(false)
    expect(isTerminalFailure(statePermanent)).toBe(true)
    expect(isRetryableFailure(statePermanent)).toBe(false)
  })

  it('partitions cleanly: retryable ⊎ terminal = all failures', () => {
    const rows = [downloadedRetryable, downloadedTerminal, stateFailed, statePermanent, cleanDownloaded]
    const all = rows.filter(failSubTabPredicate('all'))
    const retryable = rows.filter(failSubTabPredicate('retryable'))
    const terminal = rows.filter(failSubTabPredicate('terminal'))

    expect(all).toHaveLength(4) // cleanDownloaded excluded
    expect(retryable).toHaveLength(2) // downloadedRetryable + stateFailed
    expect(terminal).toHaveLength(2) // downloadedTerminal + statePermanent
    // No row is in both sub-tabs.
    expect(retryable.filter((r) => terminal.includes(r))).toHaveLength(0)
    expect(retryable.length + terminal.length).toBe(all.length)
  })
})
