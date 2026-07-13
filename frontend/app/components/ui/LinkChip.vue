<script setup lang="ts">
import { computed } from 'vue'
import { isHttpUrl } from '../../utils/safeUrl'

/**
 * LinkChip — one external-link pill: a leading site icon in a tinted square, the
 * label, and a trailing arrow that slides in on hover. It opens in a new tab
 * (`target="_blank"` + `rel="noopener noreferrer"`) and every href is gated
 * through `safeUrl.isHttpUrl`, so a non-http(s) URL never becomes a live link —
 * it renders as an inert, dimmed pill instead.
 *
 * The icon is resolved in three steps: the explicit `icon` prop, else a known
 * site derived from `label` (AniList, MAL, MangaDex, MangaUpdates, Anime-Planet,
 * Official, …), else a generic link glyph.
 *
 *   - `label` (required): the pill text + the site-icon hint.
 *   - `url`   (required): the destination (validated).
 *   - `icon`: an explicit lucide name to override the derived one.
 */
const props = defineProps<{
  /** The pill text (also the site-icon hint when `icon` is absent). */
  label: string
  /** Destination URL — validated; a non-http(s) value renders inert. */
  url: string
  /** Explicit lucide icon name; overrides the label-derived icon. */
  icon?: string
}>()

// Known destinations → a tasteful lucide glyph (lucide ships no brand marks, so
// these are meaning-matched, not logos). Order matters: check the longer/rarer
// keys first so "mangaupdates" wins over a bare "manga".
const SITE_ICONS: { match: string, icon: string }[] = [
  { match: 'mangaupdates', icon: 'lucide:refresh-cw' },
  { match: 'mangadex', icon: 'lucide:book-open' },
  { match: 'anilist', icon: 'lucide:list-checks' },
  { match: 'animeplanet', icon: 'lucide:orbit' },
  { match: 'myanimelist', icon: 'lucide:book-marked' },
  { match: 'mal', icon: 'lucide:book-marked' },
  { match: 'kitsu', icon: 'lucide:cat' },
  { match: 'official', icon: 'lucide:badge-check' },
  { match: 'twitter', icon: 'lucide:at-sign' },
  { match: 'website', icon: 'lucide:globe' },
]

/** Resolve `label` to a lucide icon; generic link glyph when nothing matches. */
const iconForLabel = (label: string): string => {
  const key = label.toLowerCase().replace(/[^a-z0-9]/g, '')
  return SITE_ICONS.find((s) => key.includes(s.match))?.icon ?? 'lucide:external-link'
}

const resolvedIcon = computed(() => props.icon ?? iconForLabel(props.label))
const safeHref = computed(() => (isHttpUrl(props.url) ? props.url : undefined))
</script>

<template>
  <a
    v-if="safeHref"
    class="link-chip"
    :href="safeHref"
    target="_blank"
    rel="noopener noreferrer"
  >
    <span class="link-chip__icon"><Icon :name="resolvedIcon" /></span>
    <span class="link-chip__label">{{ label }}</span>
    <Icon class="link-chip__arrow" name="lucide:arrow-up-right" />
  </a>
  <span v-else class="link-chip link-chip--inert" :title="`Invalid link: ${url}`">
    <span class="link-chip__icon"><Icon :name="resolvedIcon" /></span>
    <span class="link-chip__label">{{ label }}</span>
  </span>
</template>

<style scoped>
.link-chip {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px 6px 7px;
  border-radius: var(--radius-pill);
  border: 1px solid var(--border2);
  background: var(--surface2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  text-decoration: none;
  white-space: nowrap;
  transition: border-color 0.15s, background 0.15s, color 0.15s;
}

.link-chip__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-pill);
  background: var(--accentSoft);
  color: var(--accentBright);
  font-size: 13px;
  transition: background 0.15s, color 0.15s;
}

.link-chip__label {
  line-height: 1.4;
}

.link-chip__arrow {
  font-size: 13px;
  color: var(--faint);
  opacity: 0;
  transform: translate(-3px, 2px);
  transition: opacity 0.15s, transform 0.15s, color 0.15s;
}

.link-chip:hover {
  border-color: var(--accent);
  background: var(--surface3);
  color: var(--accentBright);
}

.link-chip:hover .link-chip__icon {
  background: var(--accent);
  color: var(--cover-text);
}

.link-chip:hover .link-chip__arrow {
  opacity: 1;
  transform: translate(0, 0);
  color: var(--accentBright);
}

.link-chip:focus-visible {
  outline: none;
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

/* Inert (invalid URL): looks like a chip but is not a link + is dimmed. */
.link-chip--inert {
  opacity: 0.55;
  cursor: not-allowed;
}
</style>
