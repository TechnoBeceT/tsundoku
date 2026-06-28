/**
 * useHealth – error-surfacing contract.
 *
 * Pins the behaviour that a failed GET /api/health sets error.value to the
 * message string. Non-vacuous: if the catch block in useHealth were removed,
 * error.value would stay null and this test would fail.
 *
 * vi.mock is hoisted by Vitest's transform so the mock is in place before
 * useHealth.ts is evaluated, regardless of import order in this file.
 */
import { describe, it, expect, vi } from 'vitest'
import { useHealth } from './useHealth'

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockResolvedValue({ error: { message: 'boom' }, data: undefined }),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useHealth – error surfacing', () => {
  it('sets error.value when GET /api/health fails', async () => {
    const { error } = useHealth()
    // The initial load fires as a fire-and-forget void; wait until it settles.
    await vi.waitFor(() => {
      expect(error.value).toBe('Failed to load health data')
    })
  })
})
