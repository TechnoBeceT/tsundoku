/**
 * durationConversion — shared DurationValue ↔ integer-seconds/minutes
 * mappers, extracted so every settings composable that maps a backend field
 * typed as a plain integer unit onto the screen's friendly `{ value, unit }`
 * picker (e.g. useFlareSolverrSettings.ts) never re-derives the same four
 * conversions (§2 DRY).
 */
import type { DurationValue } from '~/components/screens/settings.types'

/** Integer seconds → DurationValue (expressed in seconds). */
export function fromSecondsDuration(seconds: number): DurationValue {
  return { value: seconds, unit: 's' }
}

/** Integer minutes → DurationValue (expressed in minutes). */
export function fromMinutesDuration(minutes: number): DurationValue {
  return { value: minutes, unit: 'm' }
}

/** DurationValue → integer seconds (for an API field typed in seconds). */
export function toSecondsDuration(d: DurationValue): number {
  if (d.unit === 'h') return d.value * 3600
  if (d.unit === 'm') return d.value * 60
  return d.value
}

/** DurationValue → integer minutes (for an API field typed in minutes). */
export function toMinutesDuration(d: DurationValue): number {
  if (d.unit === 'h') return d.value * 60
  if (d.unit === 's') return Math.round(d.value / 60)
  return d.value
}
