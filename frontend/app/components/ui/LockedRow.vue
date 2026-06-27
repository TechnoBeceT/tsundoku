<script setup lang="ts">
/**
 * LockedRow — a read-only "set at deploy time" key/value row.
 *
 * A padlock-fronted label on the left, a monospace value on the right, split
 * across a top-bordered row. Used for deploy-time facts (env-sourced config)
 * the owner can see but not edit in the UI.
 *
 *   - `label` (required): the field name shown beside the lock icon.
 *   - `value` (required): the read-only value, rendered monospace.
 *   - `muted` (default false): dim the value (e.g. a masked secret `••••••••`).
 */
withDefaults(defineProps<{
  /** Field name shown beside the lock icon. */
  label: string
  /** Read-only value, rendered monospace. */
  value: string
  /** Dim the value (for masked/placeholder values). */
  muted?: boolean
}>(), {
  muted: false,
})
</script>

<template>
  <div class="lrow">
    <span class="lrow__label">
      <!-- Inline padlock glyph, tinted via currentColor (the muted label colour). -->
      <svg
        class="lrow__lock"
        width="15"
        height="15"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.9"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <rect x="4" y="11" width="16" height="10" rx="2" />
        <path d="M8 11V7a4 4 0 0 1 8 0v4" />
      </svg>
      {{ label }}
    </span>
    <span class="lrow__val" :class="{ 'lrow__val--muted': muted }">{{ value }}</span>
  </div>
</template>

<style scoped>
.lrow {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-top: 1px solid var(--border);
}

.lrow__label {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: 13.5px;
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.lrow__lock {
  display: inline-flex;
  color: var(--muted);
}

.lrow__val {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  color: var(--text);
}

.lrow__val--muted {
  color: var(--muted);
}
</style>
