<script setup lang="ts">
/**
 * SourceFilterChips — a wrapping row of toggle pills that restrict a source
 * search to a chosen subset of sources. Extracted verbatim from the Adopt
 * wizard's inline Stage-1 filter (`screens/Import.vue`) so the Series-Detail
 * "Add a source" dialog can reuse the exact same chip UX (§2 DRY) — one shared
 * component, one set of styles.
 *
 * `sources` is the list to render; `selected` is the array of currently-active
 * source IDs (v-model:selected). Clicking a chip toggles its ID and emits the
 * NEW array via `update:selected` — the component holds no state of its own, so
 * the parent owns the selection.
 *
 * Kept type-dependency-free like its sibling `ui/` atoms: it declares a minimal
 * local `ChipSource` shape ({ id, name }) instead of importing the domain
 * `Source` type, so any caller with an id+name list can drive it. It references
 * only design tokens, so it reads correctly in both themes.
 *
 * SCROLL MODEL (QCAT-265 §2.6). Two host shapes, chosen by the caller via
 * `bounded` (default true — every existing consumer unchanged):
 *   - `bounded` (default): with 40+ real sources an unbounded `flex-wrap` cloud
 *     runs to ~20 rows — taller than a phone viewport, and in a modal/dialog
 *     (MatchSourceDialog, MatchDiskProviderDialog) it would dominate the surface.
 *     So the cloud caps its own height (`max-height` + `overflow-y: auto`) into a
 *     compact internally-scrolling box. This is the right shape for a MODAL, which
 *     is bounded by nature (§2.6.2). The fix lives here once (§2 DRY).
 *   - `bounded="false"`: NO cap, NO inner-scroll — the cloud GROWS with its
 *     content and the DOCUMENT scrolls. The Adopt wizard (`screens/Import.vue`)
 *     uses this INSIDE a `ui/DisclosurePanel` (QCAT-265 treatment #2, the owner's
 *     "exclude sources … open/close that list is more smart"): a long list that is
 *     in the way is tamed by COLLAPSING it, never by a nested scroll band.
 */
interface ChipSource {
  /** Suwayomi source ID (string — a 64-bit int on the wire). */
  id: string
  /** Human-readable source name shown on the chip. */
  name: string
}

const props = withDefaults(defineProps<{
  /** The sources to render as toggle chips. */
  sources: ChipSource[]
  /** The currently-selected source IDs (v-model:selected). */
  selected: string[]
  /** Leading label shown before the chips; defaults to "Limit to:", "" hides it. */
  label?: string
  /**
   * Cap the cloud to a compact internally-scrolling box (default true — the
   * modal/dialog shape). Set false for the disclosure host (Import), where the
   * cloud grows and the document scrolls (QCAT-265 treatment #2).
   */
  bounded?: boolean
}>(), {
  label: 'Limit to:',
  bounded: true,
})

const emit = defineEmits<{
  /** The selection changed — carries the new array of selected source IDs. */
  'update:selected': [ids: string[]]
}>()

const toggle = (id: string): void => {
  emit('update:selected', props.selected.includes(id)
    ? props.selected.filter(x => x !== id)
    : [...props.selected, id])
}
</script>

<template>
  <div class="imp-filter" :class="{ 'imp-filter--bounded': bounded }">
    <span v-if="label" class="imp-filter__label">{{ label }}</span>
    <button
      v-for="s in sources"
      :key="s.id"
      type="button"
      class="imp-chip"
      :class="{ 'imp-chip--on': selected.includes(s.id) }"
      @click="toggle(s.id)"
    >
      {{ s.name }}
    </button>
  </div>
</template>

<style scoped>
/* The chip cloud. Spacing on the fluid ladder (byte-identical at the 16px
 * anchor). GROWS by default; `.imp-filter--bounded` (the modal/dialog shape)
 * caps it into a compact internally-scrolling box (see the doc comment above). */
.imp-filter {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4375rem; /* 7px @16 — off-ladder, byte-identical rem literal */
  align-items: center;
  align-content: flex-start;
  margin-bottom: var(--space-2xl-tight); /* 20px @16 */
}

/* Caps the cloud to roughly 3-4 rows at any width (modal/dialog hosts), with
 * the native scrollbar as the affordance. Import opts OUT (bounded=false). */
.imp-filter--bounded {
  max-height: clamp(5.5rem, 22vh, 12.5rem); /* 88px … 200px @16 */
  overflow-y: auto;
  overflow-x: hidden;
  padding-right: var(--space-2xs); /* 4px @16 — scrollbar gutter */
}

.imp-filter__label {
  font-size: var(--text-xs);
  color: var(--faint);
  margin-right: 0.1875rem; /* 3px @16 — off-ladder, byte-identical rem literal */
  font-weight: var(--weight-semibold);
}

.imp-chip {
  padding: var(--space-xs-tight) var(--space-md); /* 6px 12px @16 */
  border-radius: var(--radius-pill);
  border: 1px solid var(--border);
  background: var(--surface2);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  cursor: pointer;
  transition: all 0.15s;
}

.imp-chip--on {
  border-color: var(--accent);
  background: var(--accentSoft);
  color: var(--accentBright);
}
</style>
