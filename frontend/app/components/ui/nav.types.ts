/**
 * nav.types.ts — shared prop types for the navigation/flow atoms in
 * `components/ui/` (the tab-bar + stepper family). Keeping these here means the
 * atoms reference ONE definition instead of each re-declaring the same shape.
 */

/** One tab in a `SegmentedTabs` bar: a stable `key` (the emitted value), a human
 *  `label`, and an optional `count` rendered as a trailing pill. */
export interface TabItem {
  key: string
  label: string
  count?: number
}

/** One step in a `Stepper`: a stable `key` (matched against the active
 *  `current`), a `label`, and an optional `sub` line of secondary text. */
export interface StepItem {
  key: string
  label: string
  sub?: string
}
