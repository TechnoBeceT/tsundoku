import { ref, type Ref } from 'vue'

/**
 * useAsyncResource — generic async data-fetching wrapper.
 *
 * Returns `{ data, pending, error, refresh }`. Calls `refresh()` immediately
 * unless `opts.immediate` is explicitly `false`. Designed for use with the
 * generated API client in Tasks 13/14.
 */
export function useAsyncResource<T>(fetcher: () => Promise<T>, opts: { immediate?: boolean } = {}) {
  const data = ref<T | null>(null) as Ref<T | null>
  const pending = ref(false)
  const error = ref<string | null>(null)

  async function refresh(): Promise<void> {
    pending.value = true; error.value = null
    try { data.value = await fetcher() }
    catch (e) { error.value = e instanceof Error ? e.message : 'Request failed' }
    finally { pending.value = false }
  }

  if (opts.immediate !== false) void refresh()
  return { data, pending, error, refresh }
}
