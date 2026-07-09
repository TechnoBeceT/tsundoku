/**
 * findDriftedProviderIds — the FE mirror of the backend's source-identity-drift
 * detection (library.findDriftedPair / providerNameMatches + the empty-feed
 * skip). A "drifted pair" is the same physical source represented twice on one
 * series: an UNLINKED disk-origin provider (created by library import,
 * suwayomi_id=0) whose display name + scanlator match an already-attached
 * LINKED provider that has actually fetched chapters. `POST
 * /api/series/:id/providers/dedup` folds each such pair into one row.
 *
 * Detection rules (must mirror the backend so the UI never offers a pair the
 * backend would then skip):
 *   - the disk side is `linked === false`;
 *   - the linked twin is `linked === true`;
 *   - `providerName` matches trimmed + case-insensitively (backend
 *     providerNameMatches = EqualFold+TrimSpace; blank never matches);
 *   - the `scanlator` matches (null/undefined normalised to "");
 *   - the linked twin has `chapterCount > 0` (backend skips an empty-feed twin —
 *     merging it would orphan the disk chapters).
 *
 * Returns the ids of the unlinked disk providers that have such a twin (drives
 * the per-row "duplicate" badge and the panel banner count). Pure — no I/O.
 */
import type { Provider } from '~/components/screens/seriesDetail.types'

const norm = (s: string): string => s.trim().toLowerCase()

export function findDriftedProviderIds(providers: Provider[]): string[] {
  const linked = providers.filter((p) => p.linked && p.chapterCount > 0)
  const out: string[] = []
  for (const disk of providers) {
    if (disk.linked) continue
    const name = norm(disk.providerName)
    if (name === '') continue
    const twin = linked.find((l) => norm(l.providerName) === name && norm(l.scanlator) === norm(disk.scanlator))
    if (twin) out.push(disk.id)
  }
  return out
}
