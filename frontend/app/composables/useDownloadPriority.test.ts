import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useDownloadPriority } from './useDownloadPriority'

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn(), DELETE: vi.fn() }))
vi.mock('~/utils/api/client', () => ({ apiClient: api, setUnauthorizedHandler: vi.fn() }))

describe('fixed download priority actions', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.GET.mockResolvedValue({ data: [], error: null })
    api.POST.mockResolvedValue({ data: { remaining: 2 }, error: null })
    api.DELETE.mockResolvedValue({ data: { remaining: 0 }, error: null })
  })

  it('keeps failed mutations visible and guards duplicate in-flight series actions', async () => {
    const priority = useDownloadPriority()
    await priority.refresh()
    let finish!: (value: unknown) => void
    api.POST.mockReturnValue(new Promise(resolve => { finish = resolve }))
    const first = priority.setPriority('series-a', true)
    expect(priority.busyIds.value).toEqual(['series-a'])
    await priority.setPriority('series-a', true)
    expect(api.POST).toHaveBeenCalledTimes(1)
    finish({ error: { message: 'Priority could not be saved' } })
    await first
    expect(priority.busyIds.value).toEqual([])
    expect(priority.error.value).toBe('Priority could not be saved')
  })

  it('uses explicit cancel and loads authoritative remaining snapshots after success', async () => {
    const priority = useDownloadPriority()
    await priority.refresh()
    api.GET.mockResolvedValue({ data: [{ seriesId: 'series-a', seriesTitle: 'Series A', remaining: 2 }], error: null })
    await priority.setPriority('series-a', true)
    expect(priority.snapshots.value[0]?.remaining).toBe(2)
    api.GET.mockResolvedValue({ data: [], error: null })
    await priority.setPriority('series-a', false)
    expect(api.DELETE).toHaveBeenCalledWith('/api/series/{id}/download-priority', { params: { path: { id: 'series-a' } } })
    expect(priority.snapshots.value).toEqual([])
  })
})
