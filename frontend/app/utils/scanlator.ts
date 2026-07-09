/**
 * collapseUntaggedScanlator — the ONE place the "untagged bucket → empty
 * scanlator" collapse lives (shared by the Adopt wizard's `useSourceConfigure`
 * and both Series-Detail Match dialogs, so no surface re-implements it).
 *
 * The backend's `SourceBreakdown` labels a source's UNTAGGED chapters
 * (`Chapter.Scanlator == ""`) under the SOURCE NAME, but ingest's
 * `filterByScanlator` keeps only chapters whose scanlator EQUALS the requested
 * value. So adopting/linking that bucket must send scanlator `""` (= all
 * chapters from the source), NOT the source name — otherwise it matches ZERO
 * chapters and creates a silently-empty, never-downloading phantom provider.
 *
 * Returns `""` when `scanlator` is the source name (compared trimmed +
 * case-insensitively), otherwise the scanlator verbatim.
 */
export function collapseUntaggedScanlator(scanlator: string, sourceName: string): string {
  return scanlator.trim().toLowerCase() === sourceName.trim().toLowerCase() ? '' : scanlator
}
