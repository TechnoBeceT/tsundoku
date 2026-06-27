/**
 * forms.types.ts — shared prop/model types for the form + overlay atoms in
 * `components/ui/` (the button/field/dialog family). Declaring these once keeps
 * `SelectField`, `DurationInput`, and `SaveFooter` referencing ONE definition
 * instead of each re-declaring the same shape.
 */

/** One option in a `SelectField` (and any native-select-backed atom): the
 *  `value` is what the model emits, the `label` is what the option shows. */
export interface SelectOption {
  value: string
  label: string
}

/** The time unit a `DurationInput` edits: hours / minutes / seconds. */
export type DurationUnit = 'h' | 'm' | 's'

/** A `DurationInput`'s v-model: a non-negative integer `value` plus its `unit`. */
export interface DurationValue {
  value: number
  unit: DurationUnit
}

/** The async outcome a `SaveFooter` renders (the §16 loading/success/error
 *  contract for a save action). `error` carries the message shown on failure. */
export interface SaveState {
  status: 'idle' | 'saving' | 'success' | 'error'
  error?: string
}
