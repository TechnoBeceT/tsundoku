import { describe, it, expect } from 'vitest'
import { scoreSelectorFormat, scoreToDisplay, scoreToNative } from './scoreFormat'

describe('scoreSelectorFormat', () => {
  it('maps every AniList wire format to its exact ScoreSelector shape', () => {
    expect(scoreSelectorFormat('POINT_100')).toBe('point100')
    expect(scoreSelectorFormat('POINT_10')).toBe('point10')
    expect(scoreSelectorFormat('POINT_10_DECIMAL')).toBe('point10decimal')
    expect(scoreSelectorFormat('POINT_5')).toBe('point5')
    expect(scoreSelectorFormat('POINT_3')).toBe('point3')
  })

  it('maps MAL to point10 (its fixed 0-10 native scale)', () => {
    expect(scoreSelectorFormat('MAL')).toBe('point10')
  })

  it('maps Kitsu to point10decimal (the closest available shape — see the module doc comment)', () => {
    expect(scoreSelectorFormat('KITSU_RATING_TWENTY')).toBe('point10decimal')
  })

  it('falls back to point10 for a blank/unrecognized format', () => {
    expect(scoreSelectorFormat('')).toBe('point10')
    expect(scoreSelectorFormat('MANGAUPDATES')).toBe('point10')
    expect(scoreSelectorFormat('SOME_FUTURE_FORMAT')).toBe('point10')
  })
})

describe('scoreToDisplay / scoreToNative — the score-scale bug regression guard', () => {
  it('is the identity function for every non-Kitsu format (native scale already matches its ScoreSelector shape)', () => {
    for (const format of ['POINT_100', 'POINT_10', 'POINT_10_DECIMAL', 'POINT_5', 'POINT_3', 'MAL', '', 'MANGAUPDATES']) {
      expect(scoreToDisplay(85, format)).toBe(85)
      expect(scoreToNative(85, format)).toBe(85)
    }
  })

  it('halves a Kitsu native score for display (ratingTwenty 0-20 -> point10decimal 0-10)', () => {
    expect(scoreToDisplay(16, 'KITSU_RATING_TWENTY')).toBe(8)
    expect(scoreToDisplay(0, 'KITSU_RATING_TWENTY')).toBe(0)
    expect(scoreToDisplay(20, 'KITSU_RATING_TWENTY')).toBe(10)
  })

  it('doubles a Kitsu display value back to its native scale on submit', () => {
    expect(scoreToNative(8, 'KITSU_RATING_TWENTY')).toBe(16)
    expect(scoreToNative(0, 'KITSU_RATING_TWENTY')).toBe(0)
    expect(scoreToNative(10, 'KITSU_RATING_TWENTY')).toBe(20)
  })

  it('round-trips exactly for every format, including Kitsu half-point steps', () => {
    const cases: [number, string][] = [
      [92, 'POINT_100'],
      [8, 'POINT_10'],
      [7.5, 'POINT_10_DECIMAL'],
      [4, 'POINT_5'],
      [2, 'POINT_3'],
      [9, 'MAL'],
      [17, 'KITSU_RATING_TWENTY'],
    ]
    for (const [native, format] of cases) {
      expect(scoreToNative(scoreToDisplay(native, format), format)).toBe(native)
    }
  })

  it('proves the fixed-0-10 bug this module fixes: without conversion, an AniList "8" pick would silently write 8/100 instead of 80/100', () => {
    // The pre-fix behaviour treated every tracker's score editor as a fixed
    // 0-10 scale: the owner picked "8" on a point10 control and it was sent
    // straight through as the native score. scoreSelectorFormat now renders
    // AniList POINT_100 on a real 0-100 slider, so an equivalent "80%" pick
    // submits 80 (via the identity conversion for POINT_100), never 8.
    const format = 'POINT_100'
    const ownerPickedOnCorrectScale = 80
    expect(scoreToNative(ownerPickedOnCorrectScale, format)).toBe(80)
    expect(scoreToNative(ownerPickedOnCorrectScale, format)).not.toBe(8)
  })
})
