<script setup lang="ts">
import { computed, ref } from 'vue'
import Checkbox from '../ui/Checkbox.vue'
import SearchInput from '../ui/SearchInput.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import TextField from '../ui/TextField.vue'
import Toggle from '../ui/Toggle.vue'
import type { ImpersonateConfig, SourceOption } from '../screens/settings.types'

/**
 * ImpersonateCard — the toggle-gated Chrome-fingerprint image-proxy card on the
 * Server config pane (GAP-111, scoped per source by GAP-131). The enable Toggle
 * reveals the gateway URL field and the per-source opt-in list; editing anything
 * emits the full updated config (v-model) so the parent pane owns the dirty/save
 * state. Mirrors FlareSolverrCard's shape, plus the source picker.
 *
 * 🔴 THE PICKER IS THE POINT, AND IT IS OPT-IN. The proxy does NOT run the
 * source's own image processing, so a source that does not genuinely need the
 * browser fingerprint gets silently unreadable pages when it is ticked. Nothing
 * ticked = nobody uses the proxy, which is the safe default the copy states.
 * The list shows NAMES but the config carries engine source IDS — the id is the
 * identity, the name is only a label.
 *
 *   - `modelValue` (v-model): the impersonate config being edited.
 *   - `sources`: the selectable engine sources (labels for the ids).
 *
 * Emits `update:modelValue` with the full `{ ...config, <changed field> }`.
 */
const props = withDefaults(defineProps<{
  /** The impersonate config (v-model). */
  modelValue: ImpersonateConfig
  /** The selectable engine sources — labels for the ids in `modelValue.sourceIds`. */
  sources?: SourceOption[]
}>(), {
  sources: () => [],
})

const emit = defineEmits<{
  /** The config changed — carries the full updated object. */
  'update:modelValue': [value: ImpersonateConfig]
}>()

// Emit a shallow-merged copy so a single field edit never drops the rest (§16).
function patch(part: Partial<ImpersonateConfig>) {
  emit('update:modelValue', { ...props.modelValue, ...part })
}

// A library can carry dozens of sources, so the list is filterable by name once
// it stops fitting on screen.
const filter = ref('')
const visibleSources = computed(() => {
  const needle = filter.value.trim().toLowerCase()
  if (!needle) return props.sources
  return props.sources.filter(s => s.name.toLowerCase().includes(needle))
})

const selected = computed(() => new Set(props.modelValue.sourceIds))
const selectedCount = computed(() => props.modelValue.sourceIds.length)

/**
 * Tick/untick one source. Builds a NEW array rather than mutating in place (the
 * parent's copy must not change until it accepts the emitted config), keeping
 * the untouched ids in order and appending a newly ticked one. The backend
 * canonicalises the set on save, so the local order is presentational only.
 */
function toggleSource(id: string, on: boolean) {
  const next = props.modelValue.sourceIds.filter(existing => existing !== id)
  if (on) next.push(id)
  patch({ sourceIds: next })
}
</script>

<template>
  <SurfaceCard title="Chrome-fingerprint image proxy" sub="Fetch images through a browser-fingerprint proxy for the specific sources whose CDN blocks the default client">
    <template #actions>
      <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
      <Toggle :model-value="modelValue.enabled" :ariaLabel="'Enable impersonate gateway'" @update:model-value="patch({ enabled: $event })" />
    </template>
    <div v-if="modelValue.enabled" class="imp-body">
      <TextField class="field--block" label="Gateway URL" :model-value="modelValue.url" @update:model-value="patch({ url: $event })" />

      <div class="imp-sources">
        <div class="imp-sources__head">
          <span class="imp-sources__title">Sources using the proxy</span>
          <span class="imp-sources__count">{{ selectedCount }} selected</span>
        </div>
        <p class="imp-hint">
          Turn this on only for a source whose images fail to load without it. The proxy skips each
          source's own image processing, so enabling it for a source that does not need it can save
          unreadable pages. With nothing selected, no source uses the proxy.
        </p>
        <SearchInput v-if="sources.length > 8" v-model="filter" placeholder="Filter sources" />
        <p v-if="sources.length === 0" class="imp-hint imp-hint--empty">
          No sources are available from the engine right now. Anything already selected stays selected.
        </p>
        <ul v-else class="imp-sources__list">
          <li v-for="source in visibleSources" :key="source.id" class="imp-source">
            <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
            <Checkbox :model-value="selected.has(source.id)" :ariaLabel="`Use the image proxy for ${source.name}`" @update:model-value="toggleSource(source.id, $event)" />
            <span class="imp-source__name">{{ source.name }}</span>
            <span class="imp-source__lang">{{ source.lang }}</span>
          </li>
        </ul>
      </div>
    </div>
  </SurfaceCard>
</template>

<style scoped>
.imp-body {
  margin-top: 16px;
}

.field--block {
  margin-bottom: 12px;
}

.imp-hint {
  margin: 0;
  font-size: 12.5px;
  color: var(--muted);
  line-height: 1.5;
}

.imp-hint--empty {
  padding: 8px 0;
}

.imp-sources {
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.imp-sources__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
}

.imp-sources__title {
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
}

.imp-sources__count {
  font-size: 12px;
  color: var(--muted);
}

.imp-sources__list {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 260px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.imp-source {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 9px;
  border-radius: var(--radius-md);
}

.imp-source:hover {
  background: var(--surface2);
}

.imp-source__name {
  font-size: 13.5px;
  color: var(--text);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.imp-source__lang {
  margin-left: auto;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  text-transform: uppercase;
  color: var(--faint);
}
</style>
