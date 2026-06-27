<script setup lang="ts">
import { computed } from 'vue'
import type { ProviderHealth } from './seriesDetail.types'
import type { SeriesHealth } from './libraryHealth.types'

/**
 * LibraryHealth — the "what needs attention" screen. Renders ONLY the sick
 * series the backend returns (those with ≥1 stale/erroring source; completed
 * series are healthy and never appear). Per series: a clickable header (cover ·
 * title · "N unhealthy sources") and a list of its bad sources, each with a
 * health badge, a relative last-synced label, an optional chapters-behind note,
 * and the inline last-error.
 *
 * Presentation only: every series arrives via props and both actions are
 * emitted — no fetching, routing, or stores. An empty `series` array is the
 * all-clear state. `loading` shows skeletons; `refreshing` puts the rescan
 * button in-flight (§16: every action shows loading/success/error — success +
 * error land as fresh props from the parent's refetch). Token-only colours →
 * renders correctly in both themes.
 */
const props = withDefaults(defineProps<{
  /** The sick series to display; empty → all-clear state. */
  series: SeriesHealth[]
  /** When true, render skeleton cards instead of content. */
  loading?: boolean
  /** When true, the rescan action is in flight (spinner + disabled). */
  refreshing?: boolean
}>(), {
  loading: false,
  refreshing: false,
})

const emit = defineEmits<{
  /** A series row was clicked — open that series' detail view. */
  'open-series': [seriesId: string]
  /** Rescan health was clicked — the parent refetches `GET /api/health`. */
  'refresh': []
}>()

// ---- Health badge mapping (ok never appears here, but kept total) -----------
const HEALTH_BADGES: Record<ProviderHealth, { label: string, cls: string }> = {
  ok: { label: 'Healthy', cls: 'hbadge--ok' },
  stale: { label: 'Stale', cls: 'hbadge--stale' },
  erroring: { label: 'Erroring', cls: 'hbadge--erroring' },
}

// Relative-time label for the last-synced timestamp (mirrors Series Detail).
const rel = (iso: string | null): string => {
  if (iso == null) return 'never'
  const d = Date.now() - Date.parse(iso)
  const m = 60_000, h = 3_600_000, day = 86_400_000
  if (d < m) return 'just now'
  if (d < h) return `${Math.floor(d / m)}m ago`
  if (d < day) return `${Math.floor(d / h)}h ago`
  return `${Math.floor(d / day)}d ago`
}

/** A display source row: the raw provider plus the bits the template needs. */
interface SourceRow {
  id: string
  provider: string
  language: string
  badge: { label: string, cls: string }
  syncedLabel: string
  chaptersBehind: number
  hasBehind: boolean
  lastError: string
}

/** A display card: the series plus its derived header + source rows. */
interface HealthCard {
  id: string
  title: string
  initial: string
  sourceLabel: string
  sources: SourceRow[]
}

const toCard = (s: SeriesHealth): HealthCard => ({
  id: s.id,
  title: s.title,
  initial: (s.title[0] ?? '?').toUpperCase(),
  sourceLabel: `${s.sources.length} unhealthy source${s.sources.length > 1 ? 's' : ''}`,
  sources: s.sources.map((p) => ({
    id: p.id,
    provider: p.provider,
    language: p.language.toUpperCase(),
    badge: HEALTH_BADGES[p.health],
    // null last-synced reads "never synced" (not "synced never").
    syncedLabel: p.lastSyncedAt == null ? 'never synced' : `synced ${rel(p.lastSyncedAt)}`,
    chaptersBehind: p.chaptersBehind,
    hasBehind: p.chaptersBehind > 0,
    lastError: p.lastError,
  })),
})

const cards = computed(() => props.series.map(toCard))
const isEmpty = computed(() => !props.loading && cards.value.length === 0)

const skeletons = Array.from({ length: 3 }, (_, i) => i)
</script>

<template>
  <div class="health">
    <!-- Intro + rescan action -->
    <div class="health__head">
      <p class="health__intro">
        Series with at least one stale or erroring source. Completed series are treated as healthy and excluded.
      </p>
      <button
        type="button"
        class="rescan"
        :disabled="refreshing"
        @click="emit('refresh')"
      >
        <span v-if="refreshing" class="rescan__spinner" aria-hidden="true" />
        <svg v-else width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M21 12a9 9 0 1 1-2.6-6.4" />
          <path d="M21 3v6h-6" />
        </svg>
        {{ refreshing ? 'Rescanning…' : 'Rescan health' }}
      </button>
    </div>

    <!-- Loading skeletons -->
    <div v-if="loading" class="grid">
      <div v-for="n in skeletons" :key="n" class="skeleton-card" />
    </div>

    <!-- All-clear empty state -->
    <div v-else-if="isEmpty" class="allclear">
      <div class="allclear__icon">
        <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M20 6L9 17l-5-5" />
        </svg>
      </div>
      <div class="allclear__title">All clear</div>
      <div class="allclear__sub">Every source is healthy. Nothing needs your attention.</div>
    </div>

    <!-- Sick-series cards -->
    <div v-else class="grid">
      <div v-for="card in cards" :key="card.id" class="card">
        <button type="button" class="card__head" :aria-label="`Open ${card.title}`" @click="emit('open-series', card.id)">
          <span class="card__cover">
            <span class="card__initial" aria-hidden="true">{{ card.initial }}</span>
          </span>
          <span class="card__titles">
            <span class="card__title">{{ card.title }}</span>
            <span class="card__sub">{{ card.sourceLabel }}</span>
          </span>
          <svg class="card__chevron" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M9 18l6-6-6-6" />
          </svg>
        </button>

        <div v-for="src in card.sources" :key="src.id" class="source">
          <span class="source__provider">{{ src.provider }}</span>
          <span class="source__lang">{{ src.language }}</span>
          <span class="hbadge" :class="src.badge.cls"><span class="hbadge__dot" />{{ src.badge.label }}</span>
          <span class="source__synced">{{ src.syncedLabel }}</span>
          <span v-if="src.hasBehind" class="source__behind">· {{ src.chaptersBehind }} behind</span>
          <div v-if="src.lastError" class="source__error">{{ src.lastError }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.health {
  padding: 24px 30px 70px;
  background: var(--bg);
  min-height: 100%;
}

/* ---- Head: intro + rescan -------------------------------------------------- */
.health__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 20px;
}

.health__intro {
  max-width: 560px;
  margin: 0;
  font-size: var(--text-sm);
  line-height: 1.5;
  color: var(--muted);
}

.rescan {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 9px 15px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--surface);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.rescan:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accentBright);
}

.rescan:disabled {
  opacity: 0.6;
  cursor: default;
}

.rescan__spinner {
  width: 13px;
  height: 13px;
  border: 2px solid currentColor;
  border-right-color: transparent;
  border-radius: 50%;
  display: inline-block;
  animation: health-spin 0.8s linear infinite;
}

/* ---- Card grid ------------------------------------------------------------- */
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(540px, 1fr));
  gap: 14px;
  align-items: start;
}

.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-xl);
  padding: 16px;
}

.card__head {
  display: flex;
  align-items: center;
  gap: 13px;
  width: 100%;
  margin-bottom: 13px;
  padding: 0;
  border: none;
  background: none;
  text-align: left;
  cursor: pointer;
}

.card__cover {
  width: 42px;
  height: 54px;
  border-radius: var(--radius-sm);
  overflow: hidden;
  position: relative;
  flex: none;
  background: var(--cover-placeholder);
}

.card__initial {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: 22px;
  color: rgba(255, 255, 255, 0.16);
}

.card__titles {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.card__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.card__sub {
  font-size: var(--text-xs);
  color: var(--faint);
}

.card__chevron {
  flex: none;
  color: var(--faint);
}

/* ---- Source row ------------------------------------------------------------ */
.source {
  display: flex;
  align-items: center;
  gap: 9px;
  flex-wrap: wrap;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface2);
}

.source + .source {
  margin-top: 8px;
}

.source__provider {
  font-weight: var(--weight-bold);
  font-size: var(--text-base);
  color: var(--text);
}

.source__lang {
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  padding: 1px 6px;
  border-radius: var(--radius-xs);
  background: var(--surface3);
  color: var(--muted);
}

.source__synced {
  font-size: var(--text-xs);
  color: var(--faint);
}

.source__behind {
  font-size: var(--text-xs);
  color: var(--faint);
}

.source__error {
  flex-basis: 100%;
  margin-top: 3px;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--sd-hl-erroring-fg);
  word-break: break-word;
}

/* ---- Health badge (reuses the Series Detail health palette) ---------------- */
.hbadge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
}

.hbadge__dot {
  width: 6px;
  height: 6px;
  border-radius: var(--radius-pill);
  flex-shrink: 0;
  background: currentColor;
}

.hbadge--ok { color: var(--sd-hl-ok-fg); background: var(--sd-hl-ok-bg); }
.hbadge--ok .hbadge__dot { background: var(--sd-hl-ok-dot); }
.hbadge--stale { color: var(--sd-hl-stale-fg); background: var(--sd-hl-stale-bg); }
.hbadge--stale .hbadge__dot { background: var(--sd-hl-stale-dot); }
.hbadge--erroring { color: var(--sd-hl-erroring-fg); background: var(--sd-hl-erroring-bg); }
.hbadge--erroring .hbadge__dot { background: var(--sd-hl-erroring-dot); }

/* ---- All-clear empty state ------------------------------------------------- */
.allclear {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-xl);
  padding: 54px 24px;
  text-align: center;
}

.allclear__icon {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0 auto 18px;
  background: var(--sd-hl-ok-bg);
  color: var(--sd-hl-ok-dot);
}

.allclear__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-xl);
  color: var(--text);
  margin-bottom: 6px;
}

.allclear__sub {
  font-size: var(--text-sm);
  color: var(--muted);
}

/* ---- Skeletons ------------------------------------------------------------- */
.skeleton-card {
  height: 180px;
  border-radius: var(--radius-xl);
  background: var(--surface2);
  position: relative;
  overflow: hidden;
}

.skeleton-card::after {
  content: '';
  position: absolute;
  inset: 0;
  transform: translateX(-100%);
  background: linear-gradient(90deg, transparent, var(--surface3), transparent);
  animation: health-shimmer 1.4s ease-in-out infinite;
}

/* ---- Keyframes ------------------------------------------------------------- */
@keyframes health-spin {
  to { transform: rotate(360deg); }
}

@keyframes health-shimmer {
  to { transform: translateX(100%); }
}
</style>
