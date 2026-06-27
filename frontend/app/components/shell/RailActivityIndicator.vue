<script setup lang="ts">
/**
 * RailActivityIndicator — the live download-activity cluster pinned at the foot
 * of the AppShell nav rail: a pulsing accent "active downloads" glyph + count and
 * a pulsing amber "failed downloads" glyph + count. Each half is hidden when its
 * count is 0, so a quiet rail shows nothing. Presentation only — counts in, no
 * state, no emits.
 *
 * Renders the two halves as sibling roots (no wrapper) so they sit directly in
 * the rail-foot flex column, inheriting its spacing exactly as before.
 */
withDefaults(defineProps<{
  /** Active (in-flight) download count — the accent indicator (hidden at 0). */
  active?: number
  /** Failed download count — the amber indicator (hidden at 0). */
  failed?: number
}>(), {
  active: 0,
  failed: 0,
})
</script>

<template>
  <div v-if="active > 0" class="activity activity--active" :title="`${active} active downloads`">
    <Icon name="lucide:download" class="activity__icon" />
    <span class="activity__count">{{ active }}</span>
  </div>
  <div v-if="failed > 0" class="activity activity--failed" :title="`${failed} failed downloads`">
    <Icon name="lucide:triangle-alert" class="activity__icon" />
    <span class="activity__count">{{ failed }}</span>
  </div>
</template>

<style scoped>
.activity {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 1px;
}

.activity--active {
  color: var(--accentBright);
}

.activity--failed {
  color: var(--warn);
}

.activity__icon {
  width: 20px;
  height: 20px;
  animation: pulseO 1.4s ease-in-out infinite;
}

.activity__count {
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}
</style>
