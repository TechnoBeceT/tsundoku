<script setup lang="ts">
/**
 * ChapterDivider — the seam block the long-strip reader inserts between two
 * chapters (and after the last one). Shows the chapter just finished and, when
 * there is one, the chapter coming next; when `next` is absent it reads
 * "End of downloaded chapters" so the owner knows the strip has run out (§16 —
 * the end state is explicit, never a silent stop).
 *
 * Presentation-only: both chapter refs arrive via props, the divider emits nothing.
 */

/** A chapter reference shown on the divider — its display number, name, and (for
 *  the finished chapter) an optional scanlation group subtitle. */
interface DividerChapter {
  /** Display/sort number (null when unknown → shown without the "Ch." prefix). */
  number: number | null
  /** Chapter display name. */
  name: string
  /** Optional scanlation group (finished chapter only). */
  scanlator?: string
}

defineProps<{
  /** The chapter the reader has just finished (shown above the rule). */
  finished: DividerChapter
  /** The next chapter, if any; absent → the end-of-library message. */
  next?: DividerChapter
}>()

/** Formats a chapter ref as "Ch. 12 · Title" (or just the title when number is null). */
function label(ch: DividerChapter): string {
  return ch.number == null ? ch.name : `Ch. ${ch.number} · ${ch.name}`
}
</script>

<template>
  <div class="divider" role="separator">
    <div class="divider__block">
      <span class="divider__tag">Finished</span>
      <span class="divider__title">{{ label(finished) }}</span>
      <span v-if="finished.scanlator" class="divider__sub">{{ finished.scanlator }}</span>
    </div>

    <div class="divider__rule" aria-hidden="true" />

    <div v-if="next" class="divider__block">
      <span class="divider__tag divider__tag--next">Next</span>
      <span class="divider__title">{{ label(next) }}</span>
    </div>
    <div v-else class="divider__block">
      <span class="divider__end">End of downloaded chapters</span>
    </div>
  </div>
</template>

<style scoped>
.divider {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
  padding: 48px 24px;
  background: var(--surface);
  color: var(--text);
  text-align: center;
}

.divider__block {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}

.divider__tag {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--faint);
}

.divider__tag--next {
  color: var(--accentBright);
}

.divider__title {
  font-weight: var(--weight-bold);
  font-size: var(--text-md);
}

.divider__sub {
  font-size: var(--text-xs);
  color: var(--muted);
}

.divider__rule {
  width: 64px;
  height: 2px;
  border-radius: var(--radius-pill);
  background: var(--border2);
}

.divider__end {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}
</style>
