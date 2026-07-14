<script setup lang="ts">
import { computed } from 'vue'

/**
 * TrackerIcon — one native tracker's (AniList/MAL/Kitsu/MangaUpdates) small
 * square brand logo, shown next to its name on the Settings → Trackers pane
 * and the Series-Detail inline Trackers section (bound rows + "Add tracking"
 * rows). Maps the registry `trackerId` (MAL=1, AniList=2, Kitsu=3,
 * MangaUpdates=7 — see `TrackerStatus.id`'s doc comment) to a static PNG under
 * `public/tracker/` (same-origin, no CORS); an id outside that map (e.g. a
 * future tracker) falls back to a generic `lucide:link` glyph rather than a
 * broken image.
 *
 *   - `trackerId`: the registry id to resolve a logo for.
 *   - `size` (default 18): the square box size in px.
 */
const props = withDefaults(defineProps<{
  /** Registry tracker id (MAL=1, AniList=2, Kitsu=3, MangaUpdates=7). */
  trackerId: number
  /** Square box size in px. */
  size?: number
}>(), {
  size: 18,
})

// The one home for the id→logo mapping — never duplicate this switch elsewhere.
const LOGO_BY_TRACKER_ID: Record<number, { file: string, alt: string }> = {
  1: { file: 'mal.png', alt: 'MyAnimeList' },
  2: { file: 'anilist.png', alt: 'AniList' },
  3: { file: 'kitsu.png', alt: 'Kitsu' },
  7: { file: 'manga_updates.png', alt: 'MangaUpdates' },
}

const logo = computed(() => LOGO_BY_TRACKER_ID[props.trackerId] ?? null)
</script>

<template>
  <img
    v-if="logo"
    class="tracker-icon"
    :src="`/tracker/${logo.file}`"
    :alt="`${logo.alt} logo`"
    :width="size"
    :height="size"
    :style="{ width: `${size}px`, height: `${size}px` }"
    loading="lazy"
  >
  <span
    v-else
    class="tracker-icon tracker-icon--fallback"
    :style="{ width: `${size}px`, height: `${size}px` }"
    role="img"
    aria-label="Unknown tracker"
  >
    <Icon name="lucide:link" :width="Math.round(size * 0.6)" :height="Math.round(size * 0.6)" />
  </span>
</template>

<style scoped>
.tracker-icon {
  flex: none;
  border-radius: var(--radius-sm);
  object-fit: cover;
}

.tracker-icon--fallback {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: var(--surface2);
  color: var(--muted);
}
</style>
