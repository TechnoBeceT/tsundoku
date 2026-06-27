/**
 * Shared prop/data types for the AppShell chrome.
 *
 * Kept in this `.ts` (never exported from a `.vue`) so the story and any future
 * consumer can import them without the TS-server choking on a `.vue` type
 * import — same convention as `components/screens/types.ts`.
 */

/** Tone of a rail nav badge pill — `danger` (rose) is the default, `warn` (amber). */
export type NavBadgeTone = 'danger' | 'warn'

/** A count pill rendered on a nav item (e.g. unhealthy sources, failed downloads). */
export interface NavBadge {
  /** The number to show; the badge is hidden when this is 0. */
  count: number
  /** Pill colour — defaults to `danger` (rose). */
  tone?: NavBadgeTone
}

/**
 * NavItem — one button in the shell's nav rail. The shell renders WHATEVER list
 * it is given (it never hardcodes the nav or branches on a role): the caller
 * owns the item set, their order, badges, and which item is bottom-pinned.
 */
export interface NavItem {
  /** Stable key — emitted by `navigate` and matched against `activeRoute`. */
  key: string
  /** Visible label (used as the button's `title` + `aria-label`). */
  label: string
  /**
   * BARE lucide icon name — e.g. `"book"`, `"compass"`. The shell prepends the
   * `lucide:` collection prefix itself; passing `"lucide:book"` would render the
   * broken `lucide:lucide:book`.
   */
  icon: string
  /** Optional count pill on the item (hidden when count is 0). */
  badge?: NavBadge
  /** When true the item renders in the bottom-pinned group (e.g. Settings). */
  pinned?: boolean
}
