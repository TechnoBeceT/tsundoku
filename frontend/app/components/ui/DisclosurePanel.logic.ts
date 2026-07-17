/**
 * DisclosurePanel.logic — the pure decisions behind `ui/DisclosurePanel.vue`,
 * extracted so they can be unit-tested without mounting a component.
 *
 * The panel supports BOTH an uncontrolled mode (it owns its own open state,
 * seeded from `defaultOpen`) and a controlled mode (the host drives `open` via
 * `v-model:open`). `resolveOpen` is the one place that rule lives.
 */

/**
 * resolveOpen — the panel's effective open state.
 *
 * A CONTROLLED panel (the host passed an `open` prop) always renders the host's
 * value; an UNCONTROLLED one renders its own local state. A non-collapsible
 * panel is always open — it has no trigger, so nothing could ever re-open it.
 *
 * 🔴 `controlled` is compared with `== null` (loose), not `=== undefined`: an
 * `open` that arrives as `null` (a host binding a not-yet-loaded value) must
 * read as "not controlled" and fall back to local state, rather than pinning
 * the panel shut forever.
 *
 * @param collapsible whether the panel has a trigger at all
 * @param controlled  the host's `open` prop (null/undefined = uncontrolled)
 * @param local       the panel's own state (uncontrolled mode)
 */
export function resolveOpen(
  collapsible: boolean,
  controlled: boolean | null | undefined,
  local: boolean,
): boolean {
  if (!collapsible) return true
  if (controlled == null) return local
  return controlled
}

/**
 * countLabel — the header count badge's text, or "" when there is nothing to
 * show. A count of 0 is meaningful (an empty list still says "0"), so only
 * null/undefined suppress the badge.
 */
export function countLabel(count: number | string | null | undefined): string {
  if (count == null) return ''
  return String(count)
}
