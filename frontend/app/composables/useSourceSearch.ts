import { onUnmounted, ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import { mapGroup } from './importMappers'
import type { components } from '~/utils/api/schema.d.ts'
import type { SearchGroup } from '~/components/screens/import.types'

type Snapshot = components['schemas']['SearchSnapshot']
type SearchInput = { q: string, sources: string[] } | { path: string, sources: string[] }

/** One request owns its stream; closing or replacing it invalidates every later frame. */
export function useSourceSearch() {
  const groups = ref<SearchGroup[]>([])
  const searching = ref(false)
  const error = ref<string | null>(null)
  const pendingSources = ref<components['schemas']['Source'][]>([])
  const generation = ref(0)
  let controller: AbortController | null = null

  function reset(): void {
    generation.value++
    controller?.abort()
    controller = null
    groups.value = []
    pendingSources.value = []
    searching.value = false
    error.value = null
  }

  async function search(input: SearchInput): Promise<SearchGroup[]> {
    reset()
    const current = generation.value
    const abort = new AbortController()
    controller = abort
    searching.value = true
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
    let done = false
    try {
      const common = { ...(input.sources.length ? { sources: input.sources.join(',') } : {}), stream: true }
      const res = 'path' in input
        ? await apiClient.GET('/api/library/imports/match', { params: { query: { ...common, path: input.path } }, parseAs: 'stream', signal: abort.signal })
        : await apiClient.GET('/api/search', { params: { query: { ...common, q: input.q } }, parseAs: 'stream', signal: abort.signal })
      if (current !== generation.value) return []
      if (res.error || !res.data) throw new Error(res.error?.message ?? 'Search failed')
      reader = res.data.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      function frame(raw: string): void {
        let event = 'message'
        const data: string[] = []
        for (const line of raw.split(/\r?\n/)) {
          if (line.startsWith('event:')) event = line.slice(6).trim()
          if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''))
        }
        if (!data.length) return
        if (event !== 'search' && event !== 'error') return
        const value = JSON.parse(data.join('\n')) as Snapshot & { message?: string }
        if (event === 'error') throw new Error(value.message ?? 'Search failed')
        if (!Array.isArray(value.groups) || !Array.isArray(value.pendingSources) || typeof value.done !== 'boolean') throw new Error('Invalid search response')
        groups.value = value.groups.map(mapGroup)
        pendingSources.value = value.pendingSources
        done = value.done
      }
      while (!done) {
        const chunk = await reader.read()
        if (current !== generation.value) return []
        buffer += decoder.decode(chunk.value, { stream: !chunk.done })
        let boundary = /\r?\n\r?\n/.exec(buffer)
        while (boundary) {
          frame(buffer.slice(0, boundary.index))
          buffer = buffer.slice(boundary.index + boundary[0].length)
          if (done) break
          boundary = /\r?\n\r?\n/.exec(buffer)
        }
        if (chunk.done) break
      }
      if (!done) throw new Error('Search stream ended before completion. Try again.')
      return groups.value
    }
    catch (cause) {
      if (current === generation.value && !abort.signal.aborted) error.value = cause instanceof Error ? cause.message : 'Search failed'
      return []
    }
    finally {
      await reader?.cancel().catch(() => undefined)
      if (current === generation.value) {
        searching.value = false
        controller = null
      }
    }
  }

  onUnmounted(reset)
  return { groups, searching, error, pendingSources, generation, search, reset }
}
