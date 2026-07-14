/**
 * controls.types.ts — shared prop/emit types for the leaf control atoms in
 * `components/ui/` (the chip/control family). Keeping these here means several
 * atoms reference ONE definition instead of each re-declaring the same shape.
 */

/** One option in a SegmentedToggle: a stable `key` (the emitted value) + a
 *  human `label` (what the button shows). */
export interface SegmentOption {
  key: string
  label: string
}

/** Direction a ReorderControl moves an item: -1 = up (raise), 1 = down (lower).
 *  Emitted by `ReorderControl`'s `move` event. */
export type MoveDirection = -1 | 1

/**
 * The scale a ScoreSelector renders. Extracted here (rather than left inline
 * in ScoreSelector's own `defineProps`) so `utils/scoreFormat.ts` — which
 * maps a TrackBinding's native `scoreFormat` wire string to this shape — can
 * import ONE canonical definition instead of re-declaring the union.
 */
export type ScoreSelectorFormat = 'point100' | 'point10' | 'point10decimal' | 'point5' | 'point3'
