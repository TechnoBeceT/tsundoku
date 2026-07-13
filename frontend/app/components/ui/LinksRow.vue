<script setup lang="ts">
import LinkChip from './LinkChip.vue'
import type { SeriesLink } from '../screens/seriesDetail.types'

/**
 * LinksRow — a gracefully-wrapping row of external-link pills (`LinkChip`) for a
 * series' tracker + official pages. Presentation-only: it just lays out whatever
 * `links` it is handed. An empty list renders nothing, so the caller can wrap it
 * in a labelled section without an empty gap appearing.
 *
 *   - `links` (required): the links to render, in order.
 */
defineProps<{
  /** The external links to lay out (empty → renders nothing). */
  links: SeriesLink[]
}>()
</script>

<template>
  <div v-if="links.length" class="links-row">
    <LinkChip
      v-for="link in links"
      :key="link.label + link.url"
      :label="link.label"
      :url="link.url"
      :icon="link.icon"
    />
  </div>
</template>

<style scoped>
.links-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
</style>
