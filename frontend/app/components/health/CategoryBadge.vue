<script setup lang="ts">
import { computed } from 'vue'
import { categoryMeta } from '~/utils/errorCategory'

/**
 * CategoryBadge — the coloured pill for an error's classified category
 * (captcha / rate_limit / timeout / …), the taxonomy `pkg/errorclass` stores on
 * every failed event. The label + tone come from the shared `errorCategory`
 * metadata (§2 DRY), so the badge, the diagnosis engine, and any future consumer
 * agree on wording and colour. Tone → token treatment: danger = rose, warn =
 * amber, neutral = grey. Token-only → both themes.
 *
 *   - `category` (required): the raw category string (null/unknown → the
 *     neutral "Unknown" fallback).
 */
const props = defineProps<{
  /** The stored error category string; null/unmapped → the Unknown fallback. */
  category: string | null
}>()

const meta = computed(() => categoryMeta(props.category))
</script>

<template>
  <span class="cat" :class="`cat--${meta.tone}`">{{ meta.label }}</span>
</template>

<style scoped>
.cat {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.03em;
  text-transform: uppercase;
  white-space: nowrap;
}

.cat--danger {
  color: var(--danger-text);
  background: var(--danger-bg);
}

.cat--warn {
  color: var(--set-update-text);
  background: var(--set-update-bg);
}

.cat--neutral {
  color: var(--muted);
  background: var(--surface3);
}
</style>
