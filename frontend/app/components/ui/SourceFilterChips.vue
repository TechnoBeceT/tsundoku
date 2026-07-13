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
 * BOUNDED + INNER-SCROLLING (QCAT-230/231): with 40+ real sources an unbounded
 * `flex-wrap` cloud runs to ~20 rows — taller than a phone viewport. That
 * doesn't just look bad: in `screens/ScanLibrary.vue`'s fit-screen layout this
 * row sits `flex: none` above the `flex:1; min-height:0` staging list, so an
 * unbounded cloud claims all the available height and squeezes the list to
 * ZERO — the found-series rows render (data is fine) but have no room to show.
 * `.imp-filter` therefore caps its own height (`max-height` + `overflow-y:
 * auto`) so a long source list becomes a compact, internally-scrolling box
 * instead of dominating whichever screen/dialog hosts it — the fix lives here
 * once (§2 DRY) rather than in each of the 4 consumers (Import, ScanLibrary,
 * MatchSourceDialog, MatchDiskProviderDialog).
 */
interface ChipSource {
  /** Suwayomi source ID (string — a 64-bit int on the wire). */
  id: string
  /** Human-readable source name shown on the chip. */
  name: string
}

const props = defineProps<{
  /** The sources to render as toggle chips. */
  sources: ChipSource[]
  /** The currently-selected source IDs (v-model:selected). */
  selected: string[]
  /** Leading label shown before the chips; defaults to "Limit to:". */
  label?: string
}>()

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
  <div class="imp-filter">
    <span class="imp-filter__label">{{ label ?? 'Limit to:' }}</span>
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
/* Bounded + internally-scrolling (see the component doc comment above): caps
 * the chip cloud to roughly 3-4 rows at any width instead of an unbounded
 * flex-wrap cloud, with the native scrollbar as the scroll affordance. */
.imp-filter {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
  align-items: center;
  align-content: flex-start;
  margin-bottom: 20px;
  max-height: clamp(88px, 22vh, 200px);
  overflow-y: auto;
  overflow-x: hidden;
  padding-right: 4px;
}

.imp-filter__label {
  font-size: var(--text-xs);
  color: var(--faint);
  margin-right: 3px;
  font-weight: var(--weight-semibold);
}

.imp-chip {
  padding: 6px 12px;
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
