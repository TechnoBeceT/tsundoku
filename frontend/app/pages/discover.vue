<script setup lang="ts">
/**
 * Discover page — route "/discover".
 *
 * Delegates all data fetching and browse-state management to useDiscover().
 * Discover is auto-imported from app/components/screens/.
 * navigateTo is a Nuxt auto-import.
 *
 * Prop wiring:
 *   :result        — accumulated BrowseResult for the active source + listing
 *   :sources       — DiscoverSource[] for the source picker
 *   :active-source — ID of the currently browsed source
 *   :active-type   — 'popular' | 'latest'
 *   :loading       — true while a browse fetch is in flight
 *   :error         — true when the latest browse fetch failed
 *
 * Emit wiring:
 *   @set-source      → setSource(sourceId)
 *   @set-type        → setType(type)
 *   @page            → loadPage(n)
 *   @retry           → retry()
 *   @inspect         → navigateTo /import?source=&mangaId=&title= (Task 6 reads these)
 *   @adopt           → navigateTo /import?source=&mangaId=&title= (same hand-off)
 *   @open-source-link → window.open external tab (noopener)
 */
import type { DiscoverCandidate } from '~/components/screens/discover.types'

const {
  result,
  sources,
  activeSource,
  activeType,
  loading,
  error,
  setSource,
  setType,
  loadPage,
  retry,
} = useDiscover()

function openImport(candidate: DiscoverCandidate): void {
  void navigateTo({
    path: '/import',
    query: {
      source: candidate.source,
      mangaId: String(candidate.mangaId),
      title: candidate.title,
    },
  })
}
</script>

<template>
  <div class="page-discover">
    <Discover
      :result="result"
      :sources="sources"
      :active-source="activeSource"
      :active-type="activeType"
      :loading="loading"
      :error="error"
      @set-source="setSource"
      @set-type="setType"
      @page="loadPage"
      @retry="retry"
      @inspect="openImport"
      @adopt="openImport"
      @open-source-link="(c: DiscoverCandidate) => { if (isHttpUrl(c.url)) window.open(c.url, '_blank', 'noopener') }"
    />
  </div>
</template>

<style scoped>
.page-discover {
  min-height: 100%;
}
</style>
