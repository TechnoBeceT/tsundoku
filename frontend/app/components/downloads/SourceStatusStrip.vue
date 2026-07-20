<script setup lang="ts">
import SourceStatusRow from './SourceStatusRow.vue'
import type { SourceStatus } from './sourceStatus.types'

/**
 * SourceStatusStrip — the live per-source status strip: a wrapping row of
 * SourceStatusRow pills for exactly the sources that are downloading or cooling
 * right now (the backend already omits idle sources). Renders NOTHING when the
 * list is empty, so an idle library shows no strip at all. Purely presentational;
 * the parent (via useEngineStatus) owns the polling.
 */
defineProps<{
  /** The sources currently downloading / cooling (already filtered by the backend). */
  sources: SourceStatus[]
}>()
</script>

<template>
  <div v-if="sources.length > 0" class="strip" role="list" aria-label="Active sources">
    <SourceStatusRow
      v-for="s in sources"
      :key="s.sourceKey"
      role="listitem"
      :source="s"
    />
  </div>
</template>

<style scoped>
.strip {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-xs);
}
</style>
