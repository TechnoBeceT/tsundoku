<script setup lang="ts">
import { computed } from 'vue'
import IconButton from '../ui/IconButton.vue'
import Spinner from '../ui/Spinner.vue'
import type { NetworkEndpoint } from '../screens/settings.types'

/**
 * NetworkEndpointRow — one reusable network-egress endpoint row: a kind pill
 * (SOCKS / FlareSolverr), the name, a monospace connection summary (host:port for
 * SOCKS, the URL for FlareSolverr), a muted "disabled" tag when the endpoint is
 * off, and the edit + remove actions. A `busy` row dims + shows an inline spinner
 * while its mutation is in flight (§16).
 *
 *   - `endpoint`: the endpoint to render.
 *   - `busy`: this row's mutation is in flight.
 *
 * Emits `edit` and `remove`.
 */
const props = defineProps<{
  /** The endpoint to render. */
  endpoint: NetworkEndpoint
  /** This row's mutation is in flight. */
  busy: boolean
}>()

const emit = defineEmits<{
  /** Open the editor for this endpoint. */
  'edit': []
  /** Remove this endpoint. */
  'remove': []
}>()

// SOCKS shows "host:port" (+ a v4/v5 hint); FlareSolverr shows its URL. Both
// monospace so the connection target reads at a glance.
const summary = computed(() =>
  props.endpoint.kind === 'socks'
    ? `${props.endpoint.host || '—'}:${props.endpoint.port} · SOCKS${props.endpoint.socksVersion}`
    : props.endpoint.url || '—')

const kindLabel = computed(() => (props.endpoint.kind === 'socks' ? 'SOCKS' : 'FlareSolverr'))
</script>

<template>
  <div class="ep-row" :class="{ 'ep-row--busy': busy }">
    <span class="ep-row__kind" :class="`ep-row__kind--${endpoint.kind}`">{{ kindLabel }}</span>
    <div class="ep-row__body">
      <span class="ep-row__name">{{ endpoint.name }}</span>
      <span class="ep-row__summary">{{ summary }}</span>
    </div>
    <span v-if="!endpoint.enabled" class="ep-row__tag">Disabled</span>
    <Spinner v-if="busy" :size="13" tone="current" />
    <!-- eslint-disable vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
    <IconButton :ariaLabel="'Edit endpoint'" :disabled="busy" @click="emit('edit')">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" /></svg>
    </IconButton>
    <IconButton variant="danger" :ariaLabel="'Remove endpoint'" :disabled="busy" @click="emit('remove')">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" /></svg>
    </IconButton>
    <!-- eslint-enable vue/attribute-hyphenation -->
  </div>
</template>

<style scoped>
.ep-row {
  display: flex;
  align-items: center;
  gap: 11px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 11px 13px;
  margin-bottom: 9px;
}

/* In-flight row dims + blocks pointer input while its mutation runs (§16). */
.ep-row--busy {
  opacity: 0.6;
  pointer-events: none;
}

.ep-row__kind {
  flex: none;
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  padding: 3px 8px;
  border-radius: var(--radius-pill);
}

.ep-row__kind--socks {
  background: var(--accentSoft);
  color: var(--accentBright);
}

.ep-row__kind--flaresolverr {
  background: var(--set-update-bg);
  color: var(--set-update-text);
}

.ep-row__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.ep-row__name {
  font-weight: var(--weight-bold);
  font-size: 13.5px;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ep-row__summary {
  font-family: var(--font-mono);
  font-size: 11.5px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ep-row__tag {
  flex: none;
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  padding: 2px 7px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
}
</style>
