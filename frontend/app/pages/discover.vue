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
 *   @hover           → debounced loadDetails(candidate) — forces Suwayomi to
 *                      fetch the hovered card's rich metadata (author/artist/
 *                      description/genres) so the hover preview fills in; the
 *                      debounce (HOVER_DEBOUNCE_MS) absorbs a fast scrub across
 *                      the grid so it doesn't fire a fetch per card passed over.
 */
import { onBeforeUnmount } from 'vue'
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
  loadDetails,
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

/** Opens a candidate's provider-canonical link in a new tab (noopener). Lives in
 *  the script (not an inline template handler) so `window` resolves to the DOM
 *  global rather than a template binding. `isHttpUrl` blocks non-http(s) schemes. */
function openSourceLink(candidate: DiscoverCandidate): void {
  if (isHttpUrl(candidate.url)) window.open(candidate.url, '_blank', 'noopener')
}

/** Debounce window for the hover-details fetch — long enough that scrubbing
 *  across several cards in a row only fires one fetch (for the card the
 *  cursor settles on), short enough to feel instant on a deliberate hover. */
const HOVER_DEBOUNCE_MS = 200
let hoverTimer: ReturnType<typeof setTimeout> | undefined

function onHover(candidate: DiscoverCandidate): void {
  if (hoverTimer !== undefined) clearTimeout(hoverTimer)
  hoverTimer = setTimeout(() => { void loadDetails(candidate) }, HOVER_DEBOUNCE_MS)
}

onBeforeUnmount(() => {
  if (hoverTimer !== undefined) clearTimeout(hoverTimer)
})
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
      @open-source-link="openSourceLink"
      @hover="onHover"
    />
  </div>
</template>

<style scoped>
.page-discover {
  min-height: 100%;
}
</style>
