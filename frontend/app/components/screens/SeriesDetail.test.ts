/**
 * SeriesDetail — `sortedChapters` render assertion the Storybook stories don't
 * pin: a `superseded` chapter (a split-part merged into its whole, backend-side)
 * must never appear in the rendered chapter list, while every other chapter
 * still renders. The backend already excludes `superseded` from
 * `chapterCounts.total`, so this only needs to prove the FE list agrees.
 *
 * Non-vacuous: drop the `.filter((c) => c.state !== 'superseded')` from
 * `sortedChapters` in `SeriesDetail.vue` and the first assertion fails (the
 * superseded chapter's name would render).
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SeriesDetail from './SeriesDetail.vue'
import type { Chapter, SeriesDetail as SeriesDetailModel } from './seriesDetail.types'
import { categoryOptions, richSeries } from '../../fixtures/seriesDetail'

const supersededChapter: Chapter = {
  id: 'chapter-0003-1',
  chapterKey: 'ch-0003.1',
  number: 3.1,
  name: 'It’s Like a Game (part 2)',
  state: 'superseded',
  filename: '',
  pageCount: null,
  read: false,
  lastReadPage: 0,
  readAt: null,
    releaseDate: null,
}

function seriesWithSuperseded(): SeriesDetailModel {
  return {
    ...richSeries,
    chapters: [...richSeries.chapters, supersededChapter],
  }
}

describe('SeriesDetail', () => {
  it('hides a superseded chapter from the rendered chapter list', () => {
    const wrapper = mount(SeriesDetail, {
      props: {
        series: seriesWithSuperseded(),
        categoryOptions,
      },
    })

    expect(wrapper.text()).not.toContain(supersededChapter.name)
  })

  it('still renders a non-superseded chapter alongside the hidden one', () => {
    const wrapper = mount(SeriesDetail, {
      props: {
        series: seriesWithSuperseded(),
        categoryOptions,
      },
    })

    expect(wrapper.text()).toContain('The Weakest Hunter')
  })
})
