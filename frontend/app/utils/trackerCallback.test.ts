/**
 * trackerCallback — pins the sessionStorage one-shot handoff the OAuth
 * callback route relies on to learn which tracker a shared callback URL
 * belongs to (see the module's own doc comment for why).
 */
import { describe, it, expect, beforeEach } from 'vitest'
import { stashPendingTrackerId, takePendingTrackerId } from './trackerCallback'

describe('trackerCallback', () => {
  beforeEach(() => {
    sessionStorage.clear()
  })

  it('round-trips a stashed tracker id', () => {
    stashPendingTrackerId(2)
    expect(takePendingTrackerId()).toBe(2)
  })

  it('is one-shot — a second take after a stash returns null', () => {
    stashPendingTrackerId(1)
    takePendingTrackerId()
    expect(takePendingTrackerId()).toBeNull()
  })

  it('returns null when nothing was stashed', () => {
    expect(takePendingTrackerId()).toBeNull()
  })

  it('returns null for a corrupted (non-numeric) stored value rather than a garbage id', () => {
    sessionStorage.setItem('tsundoku.tracker.pendingId', 'not-a-number')
    expect(takePendingTrackerId()).toBeNull()
  })
})
