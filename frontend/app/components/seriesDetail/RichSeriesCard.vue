<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Chip from '../ui/Chip.vue'
import ChipRow from '../ui/ChipRow.vue'
import CoverImage from '../ui/CoverImage.vue'
import LinksRow from '../ui/LinksRow.vue'
import ReadMore from '../ui/ReadMore.vue'
import SelectField from '../ui/SelectField.vue'
import StatTile from '../ui/StatTile.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import Tag from '../ui/Tag.vue'
import Toggle from '../ui/Toggle.vue'
import type { SelectOption } from '../ui/forms.types'
import type { Provider, SeriesDetail } from '../screens/seriesDetail.types'

/**
 * RichSeriesCard — the Komga-style rich catalogue card for one series, and a
 * SUPERSET of SeriesHeader: cover, title + alt-titles, year/status/language/
 * needs-source badges, a clamped synopsis, the source + author credits, genre and tag chip
 * rows, the external LINKS row (the card's signature element), Tsundoku's own
 * chapter stats (Total / On disk / Wanted / Failed / Unread), AND the series
 * management controls (Monitored / Completed toggles, the Category select, and a
 * Delete button in the top-right toolbar).
 *
 * Presentation-only: everything arrives via props and every action is emitted.
 * The management contract mirrors SeriesHeader EXACTLY — same prop names
 * (`series` / `categoryOptions` / `saving`) and same emit names/payloads
 * (`changeCategory` / `toggleMonitored` / `toggleCompleted` / `requestDelete`) —
 * so the page can swap SeriesHeader → RichSeriesCard later with no rewiring.
 * Every rich catalogue field is optional, so a data-poor series still renders
 * cleanly — missing synopsis/credits/genres/tags/links simply drop out.
 *
 * There is deliberately NO download action: Tsundoku downloads automatically via
 * the Monitored toggle + the engine; there is no manual per-series download.
 *
 * Two layouts via `layout`:
 *   - `coverLeft` (default) — cover in a left column, text on the right (the
 *     Komga desktop shape).
 *   - `singleColumn` — cover on top, text stacked below (a narrow shape).
 */
const props = withDefaults(defineProps<{
  /** The series to render (summary + chapter counts + the optional rich fields). */
  series: SeriesDetail
  /** Category names for the recategorize select (dynamic, user-defined list). */
  categoryOptions: string[]
  /** True while an inline mutation is in flight — disables the controls. */
  saving?: boolean
  /** Layout shape — side-by-side or stacked. */
  layout?: 'coverLeft' | 'singleColumn'
}>(), {
  saving: false,
  layout: 'coverLeft',
})

const emit = defineEmits<{
  /** The category select changed — carries the new category name. */
  changeCategory: [category: string]
  /** The Monitored toggle flipped — carries the NEW value. */
  toggleMonitored: [monitored: boolean]
  /** The Completed toggle flipped — carries the NEW value. */
  toggleCompleted: [completed: boolean]
  /** The Metadata button was pressed (→ the parent opens the Identify modal). */
  openMetadata: []
  /** The cover's "Change cover" affordance was pressed (→ the parent opens the CoverPickerModal). */
  openCoverPicker: []
  /** The Delete button was pressed (→ the parent opens the delete dialog). */
  requestDelete: []
  /** The Trackers button was pressed (→ the parent opens the Tracking panel, Phase 3d). ADDITIVE — every existing prop/emit above is unchanged. */
  openTrackers: []
}>()

// The authoritative source for display: the pinned metadata provider if set,
// else the highest-importance one. Null for a source-less (e.g. disk-only) series.
const displayProvider = computed<Provider | null>(() => {
  const list = props.series.providers
  if (!list.length) return null
  if (props.series.metadataProviderId) {
    const pinned = list.find((p) => p.id === props.series.metadataProviderId)
    if (pinned) return pinned
  }
  return [...list].sort((a, b) => b.importance - a.importance)[0] ?? null
})

// "Source · Scanlator" from the display provider (scanlator appended only when set).
const sourceLine = computed(() => {
  const p = displayProvider.value
  if (!p) return ''
  return p.scanlator ? `${p.providerName} · ${p.scanlator}` : p.providerName
})

const primaryLanguage = computed(() => displayProvider.value?.language.toUpperCase() ?? '')
const authorsLine = computed(() => (props.series.authors ?? []).join(', '))
const altTitlesLine = computed(() => (props.series.altTitles ?? []).join('  ·  '))
const genres = computed(() => props.series.genres ?? [])
const tags = computed(() => props.series.tags ?? [])
const links = computed(() => props.series.links ?? [])
const counts = computed(() => props.series.chapterCounts)

// {value,label} pairs for the category SelectField (the value IS the name).
const categorySelectOptions = computed<SelectOption[]>(() =>
  props.categoryOptions.map((c) => ({ value: c, label: c })),
)

// Status word → Tag tone. Unknown words fall back to neutral.
const statusTone = computed<'neutral' | 'accent' | 'success' | 'warn' | 'danger'>(() => {
  switch ((props.series.status ?? '').toLowerCase()) {
    case 'completed':
    case 'finished': return 'success'
    case 'ongoing':
    case 'releasing':
    case 'publishing': return 'accent'
    case 'hiatus': return 'warn'
    case 'cancelled':
    case 'canceled':
    case 'discontinued': return 'danger'
    default: return 'neutral'
  }
})

const hasBadges = computed(
  () => props.series.year !== undefined || !!props.series.status || !!primaryLanguage.value || props.series.needsSource,
)
</script>

<template>
  <SurfaceCard>
    <div class="rich" :class="`rich--${layout}`">
      <!-- Cover -->
      <div class="rich__cover">
        <CoverImage :src="series.coverUrl" :alt="`${series.title} cover`" placeholder="brand" />
        <!-- "Change cover" affordance: a subtle overlay button revealed on hover
             OR keyboard focus (focus-within), sitting over the whole cover. Opens
             the CoverPickerModal via the parent. It overlays the CoverImage, so
             it works over a real cover AND the branded placeholder alike. -->
        <button
          type="button"
          class="rich__cover-change"
          @click="emit('openCoverPicker')"
        >
          <Icon name="lucide:image" />
          <span>Change cover</span>
        </button>
      </div>

      <!-- Body -->
      <div class="rich__body">
        <!-- Title block + top-right management toolbar -->
        <div class="rich__head">
          <div class="rich__titleblock">
            <Chip variant="category">{{ series.category }}</Chip>
            <h2 class="rich__title">{{ series.title }}</h2>
            <p v-if="altTitlesLine" class="rich__alts">{{ altTitlesLine }}</p>
          </div>
          <div class="rich__toolbar">
            <!-- The "Identify" metadata-match button sits to the LEFT of Delete;
                 the toolbar is a flex row so both sit side by side without
                 reflowing Delete. Opens the MetadataIdentifyModal via the parent. -->
            <AppButton variant="ghost" size="sm" @click="emit('openMetadata')">
              <template #icon><Icon name="lucide:scan-search" /></template>
              Metadata
            </AppButton>
            <AppButton variant="ghost" size="sm" @click="emit('openTrackers')">
              <template #icon><Icon name="lucide:link" /></template>
              Trackers
            </AppButton>
            <AppButton variant="danger-ghost" size="sm" @click="emit('requestDelete')">
              <template #icon><Icon name="lucide:trash-2" /></template>
              Delete
            </AppButton>
          </div>
        </div>

        <!-- Year / status / language / needs-source badges. NeedsSource is
             deliberately part of this always-rendered badge row rather than
             gated on the cover, so it stays visible EVEN WHEN the series has
             a metadata cover (handover 2026-07-13#15 — cover ⊥ source). -->
        <div v-if="hasBadges" class="rich__badges">
          <Tag v-if="series.year !== undefined" tone="neutral">
            <template #icon><Icon name="lucide:calendar" /></template>
            {{ series.year }}
          </Tag>
          <Tag v-if="series.status" :tone="statusTone">{{ series.status }}</Tag>
          <Chip v-if="primaryLanguage" variant="language">{{ primaryLanguage }}</Chip>
          <Tag v-if="series.needsSource" tone="warn">
            <template #icon><Icon name="lucide:triangle-alert" /></template>
            Needs source
          </Tag>
        </div>

        <!-- Synopsis -->
        <ReadMore v-if="series.description" :text="series.description" :lines="4" />

        <!-- Credits -->
        <div v-if="sourceLine || authorsLine" class="rich__credits">
          <div v-if="authorsLine" class="rich__credit">
            <Icon class="rich__credit-icon" name="lucide:feather" />
            <span class="rich__credit-label">Story</span>
            <span class="rich__credit-value">{{ authorsLine }}</span>
          </div>
          <div v-if="sourceLine" class="rich__credit">
            <Icon class="rich__credit-icon" name="lucide:library" />
            <span class="rich__credit-label">Source</span>
            <span class="rich__credit-value">{{ sourceLine }}</span>
          </div>
        </div>

        <!-- Genres + tags -->
        <ChipRow :items="genres" label="Genres" variant="neutral" />
        <ChipRow :items="tags" label="Tags" variant="neutral" />

        <!-- Links (the signature element) -->
        <div v-if="links.length" class="rich__links">
          <div class="rich__links-head">
            <Icon name="lucide:link" />
            <span>Links</span>
          </div>
          <LinksRow :links="links" />
        </div>

        <!-- Footer: chapter stats + management controls -->
        <div class="rich__footer">
          <div class="rich__stats">
            <div class="rich__stat">
              <StatTile label="Total" :value="counts.total" />
            </div>
            <div class="rich__stat">
              <StatTile label="On disk" :value="counts.downloaded" tone="var(--sd-stat-disk)" />
            </div>
            <div class="rich__stat">
              <StatTile label="Wanted" :value="counts.wanted" tone="var(--accentBright)" />
            </div>
            <div class="rich__stat">
              <StatTile label="Failed" :value="counts.failed" tone="var(--danger-bright)" />
            </div>
            <div class="rich__stat">
              <StatTile label="Unread" :value="counts.unread" />
            </div>
          </div>

          <!-- Monitored / Completed toggles + Category select (reuses the exact
               SeriesHeader controls so the emit contract matches). -->
          <div class="rich__controls">
            <!-- eslint-disable vue/attribute-hyphenation -->
            <!-- camelCase :ariaLabel binds Toggle's REQUIRED prop; kebab :aria-label
                 routes to the native attr, leaving it unset (vue-tsc error). -->
            <div class="rich__control">
              <Toggle
                :model-value="series.monitored"
                :disabled="saving"
                :ariaLabel="'Monitored'"
                @update:model-value="emit('toggleMonitored', $event)"
              />
              <div>
                <div class="rich__control-title">Monitored</div>
                <div class="rich__control-hint">Auto-check for new chapters</div>
              </div>
            </div>

            <div class="rich__control">
              <Toggle
                :model-value="series.completed"
                :disabled="saving"
                :ariaLabel="'Completed'"
                @update:model-value="emit('toggleCompleted', $event)"
              />
              <div>
                <div class="rich__control-title">Completed</div>
                <div class="rich__control-hint">Mark finished · skip sweeps</div>
              </div>
            </div>
            <!-- eslint-enable vue/attribute-hyphenation -->

            <label class="rich__control rich__control--category">
              <span class="rich__control-catlabel">Category</span>
              <SelectField
                :model-value="series.category"
                :options="categorySelectOptions"
                :disabled="saving"
                aria-label="Category"
                @update:model-value="emit('changeCategory', $event)"
              />
            </label>
          </div>
        </div>
      </div>
    </div>
  </SurfaceCard>
</template>

<style scoped>
/* The row⇄column shape, cover width/alignment, stats column count, and the
 * category control's margin are all switched via custom properties (each
 * consuming declaration lives ONCE, on the base selector below) so the
 * `layout="singleColumn"` prop AND the <900px responsive breakpoint can flip
 * the exact same switches without duplicating any rule body — see the
 * `@media (max-width: 900px)` block at the end of this file, which forces
 * this SAME single-column shape (the one the `SingleColumn` story renders)
 * regardless of the `layout` prop. */
.rich {
  display: flex;
  gap: 24px;
  max-width: 100%;
  flex-direction: var(--rich-direction, row);
  align-items: var(--rich-align, stretch);
}

.rich--singleColumn {
  --rich-direction: column;
  --rich-align: stretch;
  --rich-cover-width: 190px;
  --rich-cover-align: center;
  --rich-stats-cols: repeat(2, minmax(0, 1fr));
  --rich-cat-margin: 0;
}

/* ---- Cover ---------------------------------------------------------------- */
.rich__cover {
  position: relative;
  flex: none;
  width: var(--rich-cover-width, 208px);
  max-width: 100%;
  /* align-self:flex-start stops the cover column stretching to the (taller) text
     column's height — otherwise this bordered box grows past the CoverImage's
     0.72-aspect picture, leaving an empty box below it (and a seam on the
     placeholder). With flex-start the box hugs the image's natural height. */
  align-self: var(--rich-cover-align, flex-start);
  border-radius: var(--radius-lg);
  overflow: hidden;
  border: 1px solid var(--border);
}

/* The "Change cover" affordance — a frosted button on a bottom scrim, hidden at
   rest and revealed on hover of the cover OR keyboard focus of the button, so it
   never competes with the poster until the owner reaches for it. */
.rich__cover-change {
  position: absolute;
  inset: auto 0 0 0;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  padding: 11px 12px;
  border: none;
  background: var(--cover-frost);
  backdrop-filter: blur(4px);
  color: var(--cover-text);
  font-family: var(--font-sans);
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  cursor: pointer;
  opacity: 0;
  transform: translateY(100%);
  transition: opacity 0.15s, transform 0.15s;
}

.rich__cover:hover .rich__cover-change,
.rich__cover-change:focus-visible {
  opacity: 1;
  transform: translateY(0);
}

.rich__cover-change:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

/* ---- Body ----------------------------------------------------------------- */
.rich__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* Title block (left) + management toolbar (top-right). flex-wrap lets the
 * toolbar drop to its own line rather than overflow when the title block has
 * been squeezed to a narrow body (singleColumn / <900px) and the two toolbar
 * buttons no longer fit beside it — a no-op at the wide desktop shape, where
 * there's always room for both on one line. */
.rich__head {
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 12px;
}

.rich__titleblock {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
}

/* A flex row so a future "metadata source" button can sit left of Delete. */
.rich__toolbar {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
}

.rich__title {
  margin: 0;
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: var(--text-3xl);
  line-height: 1.12;
  color: var(--text);
}

.rich__alts {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: var(--weight-medium);
  color: var(--faint);
  line-height: 1.4;
  /* A long alt-title list can be one unbroken string with no space to wrap
   * at (CJK titles, long romanizations) — let it break mid-word rather than
   * overflow the (now possibly narrow) title block. */
  overflow-wrap: anywhere;
}

/* ---- Badges --------------------------------------------------------------- */
.rich__badges {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

/* ---- Credits -------------------------------------------------------------- */
.rich__credits {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.rich__credit {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: var(--text-sm);
  line-height: 1.4;
}

.rich__credit-icon {
  flex: none;
  align-self: center;
  color: var(--faint);
  font-size: 14px;
}

.rich__credit-label {
  flex: none;
  min-width: 46px;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  color: var(--faint);
}

.rich__credit-value {
  color: var(--text);
  font-weight: var(--weight-semibold);
}

/* ---- Links (signature) ---------------------------------------------------- */
.rich__links {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding-top: 16px;
  border-top: 1px solid var(--border);
}

.rich__links-head {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  color: var(--accentBright);
}

/* ---- Footer: stats + management controls ---------------------------------- */
.rich__footer {
  display: flex;
  flex-direction: column;
  gap: 16px;
  margin-top: auto;
  padding-top: 16px;
  border-top: 1px solid var(--border);
}

/* Five tiles across in the wide right column; singleColumn / <900px fold to
 * 2-up via the shared --rich-stats-cols switch (set on `.rich`, inherited
 * down — see `.rich--singleColumn` above and the `@media` block below). */
.rich__stats {
  display: grid;
  grid-template-columns: var(--rich-stats-cols, repeat(5, minmax(0, 1fr)));
  gap: 10px;
}

.rich__stat {
  padding: 11px 13px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--surface2);
}

/* ---- Controls (toggles + category) ---------------------------------------- */
.rich__controls {
  display: flex;
  align-items: center;
  gap: 22px;
  flex-wrap: wrap;
}

.rich__control {
  display: flex;
  align-items: center;
  gap: 11px;
}

.rich__control--category {
  margin-left: var(--rich-cat-margin, auto);
  gap: 9px;
  cursor: pointer;
}

.rich__control-title {
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.rich__control-hint {
  font-size: var(--text-xs);
  color: var(--faint);
}

.rich__control-catlabel {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--faint);
}

/* ---- Responsive ------------------------------------------------------------
 * Below 900px the fixed-width cover can no longer sit beside the text column
 * without forcing the card (and everything above it, up through AppShell)
 * wider than the viewport — that horizontal overflow is what was breaking
 * vertical scroll on mobile. Force the SAME single-column shape the
 * `layout="singleColumn"` prop renders (the `SingleColumn`/`LongSingleColumn`
 * stories) regardless of which `layout` the caller passed, by flipping the
 * exact same custom-property switches `.rich--singleColumn` sets above — each
 * one is read by exactly one declaration (`.rich`/`.rich__cover`/
 * `.rich__stats`/`.rich__control--category`), so nothing here duplicates a
 * rule body. Setting them on `.rich` is enough: custom properties inherit
 * down to every descendant that reads them. */
@media (max-width: 900px) {
  .rich {
    --rich-direction: column;
    --rich-align: stretch;
    --rich-cover-width: 190px;
    --rich-cover-align: center;
    --rich-stats-cols: repeat(2, minmax(0, 1fr));
    --rich-cat-margin: 0;
  }

  /* `.rich__head`'s flex-wrap (see the rule above) is a no-op on desktop —
   * there's always room for the titleblock + toolbar on one line. On the
   * narrow single-column body it DOES wrap, but `.rich__titleblock` is still
   * a `flex: 1` row item measured against the (now absent) toolbar sibling,
   * so a wrapped flex item shrinks to its own min-content width instead of
   * the full row — collapsing the title to a narrow column (3-line-wrapped
   * "A Man's Man", one-word-per-line alt titles) even though the whole card
   * width is available below it. Forcing the titleblock onto its own
   * full-width row fixes that; the toolbar (Metadata/Delete) simply wraps
   * beneath it, unaffected. */
  .rich__titleblock {
    flex-basis: 100%;
  }
}
</style>
