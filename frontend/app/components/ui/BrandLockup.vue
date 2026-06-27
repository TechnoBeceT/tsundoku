<script setup lang="ts">
import { computed } from 'vue'
import BrandMark from './BrandMark.vue'

/**
 * BrandLockup — the full Tsundoku lockup: the BrandMark beside the "Tsundoku"
 * wordmark (display font, black weight) and, optionally, the 積ん読 + "Manga
 * library manager" subtitle row. Mirrors the prototype's primary lockup.
 *
 * Colours come from tokens so the lockup themes with the app. Toggle the
 * subtitle row off (`:subtitle="false"`) for tight spots like the nav rail
 * where only the wordmark is wanted; toggle the Japanese off independently.
 */
const props = withDefaults(defineProps<{
  /** Mark size in px; the wordmark scales relative to it. */
  size?: number
  /** Mark colour treatment, forwarded to BrandMark. */
  tone?: 'gradient' | 'mono' | 'inverse'
  /** Show the 積ん読 + tagline subtitle row. */
  subtitle?: boolean
  /** Show the 積ん読 glyph within the subtitle row. */
  japanese?: boolean
}>(), {
  size: 44,
  tone: 'gradient',
  subtitle: true,
  japanese: true,
})

// Wordmark scales off the mark size (prototype ratio ≈ mark : word = 116 : 52).
const wordSize = computed(() => Math.round(props.size * 0.86))
</script>

<template>
  <div class="lockup">
    <BrandMark :size="size" :tone="tone" />
    <div class="lockup__text">
      <div class="lockup__word" :style="{ fontSize: `${wordSize}px` }">Tsundoku</div>
      <div v-if="subtitle" class="lockup__sub">
        <span v-if="japanese" class="lockup__jp">積ん読</span>
        <span v-if="japanese" class="lockup__dot" aria-hidden="true" />
        <span class="lockup__tag">Manga library manager</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.lockup {
  display: inline-flex;
  align-items: center;
  gap: 16px;
}

.lockup__word {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  color: var(--text);
  line-height: var(--leading-tight);
  letter-spacing: -0.01em;
}

.lockup__sub {
  display: flex;
  align-items: center;
  gap: 9px;
  margin-top: 8px;
}

.lockup__jp {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-base);
  color: var(--accentBright);
}

.lockup__dot {
  width: 4px;
  height: 4px;
  border-radius: var(--radius-pill);
  background: var(--faint);
}

.lockup__tag {
  font-size: var(--text-sm);
  color: var(--muted);
  letter-spacing: 0.02em;
}
</style>
