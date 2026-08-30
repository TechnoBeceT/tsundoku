<script setup lang="ts">
import { computed } from 'vue'
import FormError from '../ui/FormError.vue'
import StatusBadge from '../ui/StatusBadge.vue'
import type { components } from '~/utils/api/schema.d.ts'

type SourceRuntimeStatus = components['schemas']['SourceRuntimeStatus']

/** Compact desired-versus-applied runtime convergence state. */
const props = defineProps<{
  runtime: SourceRuntimeStatus
}>()

const pending = computed(() => props.runtime.status === 'pending')
const badgeState = computed(() => pending.value ? 'downloading' as const : 'downloaded' as const)
const revisionLabel = computed(() => pending.value
  ? `${props.runtime.appliedRevision} → ${props.runtime.desiredRevision}`
  : `Revision ${props.runtime.appliedRevision}`)
</script>

<template>
  <div class="apply-status" role="status" aria-live="polite">
    <div class="apply-status__line">
      <StatusBadge :state="badgeState" :label="pending ? 'Pending' : 'Applied'" />
      <span class="apply-status__revision">{{ revisionLabel }}</span>
    </div>
    <FormError v-if="runtime.lastApplyError" :message="runtime.lastApplyError" />
  </div>
</template>

<style scoped>
.apply-status {
  display: grid;
  gap: var(--space-xs-tight);
  min-width: 0;
  max-width: 100%;
}

.apply-status__line {
  display: flex;
  align-items: center;
  gap: var(--space-xs);
  min-width: 0;
}

.apply-status__revision {
  min-width: 0;
  color: var(--faint);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
}
</style>
