<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Chip from '../ui/Chip.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import Spinner from '../ui/Spinner.vue'
import type { ScanEntry } from '../screens/scanLibrary.types'

/**
 * StagingRow — one staged library-scan entry in the Scan Library review table.
 * A **pending** row offers Import (disk-only) / Match / Skip; an **imported**
 * or **skipped** row is a terminal view — just its status pill, no actions
 * (there's nothing left to do with it from here). The `alreadyInDb` badge
 * flags a duplicate the owner should look at before importing.
 *
 * Any currently-in-flight mutation on THIS row dims it, replaces the action
 * buttons with a spinner, and surfaces its own inline error banner — the
 * composable tracks busy/error per staged-entry path, so several rows can act
 * independently in a large (1000+ series) library without blocking each other
 * (§16 — every operation's loading/success/error state is visible).
 *
 *   - `entry`: the staged entry to render.
 *   - `busy`: true while this row's skip/import mutation is in flight.
 *   - `error`: this row's last mutation error, or "" for none.
 *
 * Emits `import-disk-only` / `match` / `skip`, each carrying the entry's
 * `path` (its identity key) — the parent dispatches to the right composable
 * call; this row never calls the API itself.
 */
const props = withDefaults(defineProps<{
  /** The staged entry this row renders. */
  entry: ScanEntry
  /** True while this row's mutation is in flight. */
  busy?: boolean
  /** This row's last mutation error, or "" for none. */
  error?: string
}>(), {
  busy: false,
  error: '',
})

const emit = defineEmits<{
  /** Import this entry disk-only (no Suwayomi source attached). */
  'import-disk-only': [path: string]
  /** Open the cross-source match search for this entry (wired by Task 7). */
  'match': [path: string]
  /** Mark this entry skipped — it stays on disk, never re-prompted here. */
  'skip': [path: string]
}>()

/** Human label + Chip tone for each staging status. */
const STATUS_META: Record<string, { label: string, variant: 'neutral' | 'accent' }> = {
  pending: { label: 'Pending', variant: 'neutral' },
  imported: { label: 'Imported', variant: 'accent' },
  skipped: { label: 'Skipped', variant: 'neutral' },
}

// Falls back to the raw status string if the backend ever sends one this
// table doesn't know about yet, rather than rendering a blank pill.
const statusMeta = computed(() => STATUS_META[props.entry.status] ?? { label: props.entry.status, variant: 'neutral' as const })

const isPending = computed(() => props.entry.status === 'pending')

const chapterLabel = computed(() => `${props.entry.chapterCount} chapter${props.entry.chapterCount === 1 ? '' : 's'}`)
</script>

<template>
  <div class="staging-row" :class="{ 'staging-row--busy': busy }">
    <div class="staging-row__body">
      <div class="staging-row__titleline">
        <span class="staging-row__title">{{ entry.title }}</span>
        <Chip variant="category">{{ entry.category }}</Chip>
        <Chip v-if="entry.alreadyInDb" variant="accent">In library</Chip>
      </div>
      <div class="staging-row__meta">
        {{ chapterLabel }}
        <template v-if="entry.providers.length">
          · <Chip v-for="p in entry.providers" :key="p" variant="language">{{ p }}</Chip>
        </template>
        <span v-else class="staging-row__noprovider">· no known provider</span>
      </div>
      <ErrorBanner v-if="error" class="staging-row__error" :message="error" :dismissible="false" />
    </div>

    <Chip class="staging-row__status" :variant="statusMeta.variant">{{ statusMeta.label }}</Chip>

    <div v-if="isPending" class="staging-row__actions">
      <Spinner v-if="busy" :size="15" tone="accent" />
      <template v-else>
        <AppButton variant="mini" size="sm" @click="emit('import-disk-only', entry.path)">
          Import
        </AppButton>
        <AppButton variant="mini" size="sm" @click="emit('match', entry.path)">
          Match
        </AppButton>
        <AppButton variant="mini" size="sm" @click="emit('skip', entry.path)">
          Skip
        </AppButton>
      </template>
    </div>
  </div>
</template>

<style scoped>
.staging-row {
  display: flex;
  align-items: center;
  gap: 13px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 12px 14px;
}

/* In-flight row dims + drops its buttons for the spinner (§16). */
.staging-row--busy {
  opacity: 0.75;
}

.staging-row__body {
  flex: 1;
  min-width: 0;
}

.staging-row__titleline {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.staging-row__title {
  font-weight: var(--weight-bold);
  font-size: 13.5px;
  color: var(--text);
}

.staging-row__meta {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 4px;
  font-size: var(--text-sm);
  color: var(--muted);
}

.staging-row__noprovider {
  color: var(--faint);
}

.staging-row__error {
  margin-top: 8px;
}

.staging-row__status {
  flex: none;
}

.staging-row__actions {
  flex: none;
  display: flex;
  align-items: center;
  gap: 7px;
}
</style>
