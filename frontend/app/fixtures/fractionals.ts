/**
 * Story-only fixtures for the library-wide Fractionals screen. NOT imported by app
 * code — only by the Storybook stories — so the screen stays props-driven and
 * backend-free.
 *
 * The fixture mixes the states that matter: a series with a big backlog that is
 * only PARTLY removable (policy not yet fully set → removable < fractional), a
 * series where every source already ignores (removable == fractional, toggle ON),
 * and a series with nothing removable yet (removable 0, the "set policy first"
 * case). No cover urls, so the branded placeholder renders.
 */
import type { SeriesFractionals } from '../components/screens/fractionals.types'

/** A big backlog, only partly removable — one live source still carries some. */
export const partlyRemovable: SeriesFractionals = {
  seriesId: '11111111-1111-1111-1111-111111111111',
  title: "A Returner's Magic Should Be Special",
  displayName: "A Returner's Magic Should Be Special",
  category: 'Manga',
  coverUrl: '',
  fractionalCount: 6,
  removableCount: 5,
  providersTotal: 3,
  providersIgnoring: 2,
  allProvidersIgnoring: false,
}

/** Every source ignores fractionals — fully removable, toggle ON. */
export const allIgnored: SeriesFractionals = {
  seriesId: '22222222-2222-2222-2222-222222222222',
  title: 'The Beginning After The End',
  displayName: 'The Beginning After The End',
  category: 'Manhwa',
  coverUrl: '',
  fractionalCount: 12,
  removableCount: 12,
  providersTotal: 2,
  providersIgnoring: 2,
  allProvidersIgnoring: true,
}

/** Nothing removable yet — the owner must set the policy first (removable 0). */
export const policyNotSet: SeriesFractionals = {
  seriesId: '33333333-3333-3333-3333-333333333333',
  title: 'Omniscient Reader',
  displayName: 'Omniscient Reader',
  category: 'Manhwa',
  coverUrl: '',
  fractionalCount: 3,
  removableCount: 0,
  providersTotal: 2,
  providersIgnoring: 0,
  allProvidersIgnoring: false,
}

/** The default list: one of each state, in the backend's most-actionable order. */
export const fractionalSeries: SeriesFractionals[] = [allIgnored, partlyRemovable, policyNotSet]
