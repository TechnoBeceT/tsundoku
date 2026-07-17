/**
 * ResponsiveGrid.logic.ts — the pure prop→CSS-variable kernel behind
 * `ResponsiveGrid.vue`.
 *
 * Kept in a plain `.ts` (not the `.vue`) so it can be unit-tested in isolation
 * and reused without pulling in Vue — same `<Component>.logic.ts` shape as
 * `reader/ReaderStrip.logic.ts` (a distinct filename from the `.vue` so Nuxt's
 * component auto-import doesn't see a duplicate `ResponsiveGrid` name).
 *
 * The grid renders `repeat(<fill>, minmax(min(<floor>, 100%), 1fr))`:
 *   - `min(<floor>, 100%)` is the zero-overflow guard (QCAT-230): when the
 *     container is narrower than one tile the floor collapses to 100% instead of
 *     forcing horizontal overflow (which kills vertical page scroll on a phone).
 *   - `<floor>` is the min-tile expressed in `rem` (see `pxToRem`) so it rides
 *     the fluid root font-size — THIS is the "fluid minmax floor" QCAT-259 asks
 *     for, living in this one place. At the 16px desktop anchor a `rem` floor
 *     equals its original px (render-identical); on a phone it shrinks with the
 *     root so more columns survive.
 *
 * On the PHONE band that `auto-*` behaviour is WRONG, and `heldColumns` replaces
 * it (QCAT-263) — see that function's doc.
 */

/** The two column-fill modes CSS `repeat()` accepts. */
export type GridFill = 'auto-fill' | 'auto-fit'

/** Inputs mirroring `ResponsiveGrid.vue`'s props. */
export interface GridVarsInput {
  /** Desktop min-tile floor, e.g. `'186px'` (or any CSS length / `var(--…)`). */
  minTile: string
  /** Column gap — pass a `--space-*` token or a length, e.g. `'var(--space-xl)'`. */
  gap: string
  /** `auto-fill` (default) keeps empty tracks; `auto-fit` collapses them. */
  fill: GridFill
  /** Optional ≤900px min-tile override (a narrower floor for phones). */
  mobileMinTile?: string
  /** Optional ≤900px gap override. */
  mobileGap?: string
  /** Optional ≤430px HELD column count — see `heldColumns` / QCAT-263. */
  phoneColumns?: number
}

/**
 * pxToRem converts a plain `'<n>px'` string to its `rem` equivalent against the
 * 16px base (so the value is unchanged at the desktop anchor but now scales with
 * the fluid root). Any non-plain-px input — a `var(--…)` token, `%`, `rem`,
 * `clamp()` — is passed straight through untouched, so callers can hand tokens
 * (gaps) or already-fluid values without this mangling them.
 */
export function pxToRem(value: string): string {
  const match = /^(-?\d*\.?\d+)px$/.exec(value.trim())
  if (!match) return value
  return `${parseFloat(match[1]!) / 16}rem`
}

/** Builds the `repeat(<fill>, minmax(min(<floor>, 100%), 1fr))` track template. */
function columns(fill: GridFill, floor: string): string {
  return `repeat(${fill}, minmax(min(${pxToRem(floor)}, 100%), 1fr))`
}

/**
 * heldColumns builds a FIXED `repeat(<n>, minmax(0, 1fr))` track template — the
 * phone mode (QCAT-263): the column count is HELD and the tiles GROW with the
 * viewport, instead of `auto-fill` pinning the tile at its floor and dropping in
 * another column. The owner's rule: "on large mobile we need to show 3 items but
 * larger scale, like that everything could be more readable."
 *
 * The `0` floor (rather than `min(<floor>, 100%)`) is what lets the tracks shrink
 * to whatever the container gives, so QCAT-230's zero-horizontal-overflow gate
 * holds by construction at any width — a held count can never force overflow.
 *
 * `n` is floored at 1: a 0/negative count would emit `repeat(0, …)`, which is
 * invalid and would silently drop the whole track template.
 */
function heldColumns(n: number): string {
  return `repeat(${Math.max(1, Math.trunc(n))}, minmax(0, 1fr))`
}

/**
 * gridStyleVars maps the props to the inline CSS custom properties the
 * component's scoped `<style>` consumes. Each override is emitted ONLY when its
 * prop is given; otherwise the media query falls back to the wider band's var
 * (`var(--rg-cols-phone, var(--rg-cols-mobile, var(--rg-cols)))`), so a grid that
 * sets no override behaves identically at every width.
 *
 * Three bands, widest floor first:
 *   `--rg-cols`        — desktop `auto-fill`/`auto-fit` (the base rule).
 *   `--rg-cols-mobile` — ≤900px floor override (`mobileMinTile`), still auto-*.
 *   `--rg-cols-phone`  — ≤430px HELD count (`phoneColumns`), tiles grow.
 */
export function gridStyleVars(input: GridVarsInput): Record<string, string> {
  const vars: Record<string, string> = {
    '--rg-cols': columns(input.fill, input.minTile),
    '--rg-gap': pxToRem(input.gap),
  }
  if (input.mobileMinTile) vars['--rg-cols-mobile'] = columns(input.fill, input.mobileMinTile)
  if (input.mobileGap) vars['--rg-gap-mobile'] = pxToRem(input.mobileGap)
  if (input.phoneColumns !== undefined) vars['--rg-cols-phone'] = heldColumns(input.phoneColumns)
  return vars
}
