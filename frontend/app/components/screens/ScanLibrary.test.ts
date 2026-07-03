/**
 * ScanLibrary — screen-level render assertions the Storybook `play:`
 * interaction test doesn't reach (`bun run test` runs vitest, which never
 * executes Storybook play functions). Pins two behaviours a silent regression
 * could otherwise ship undetected:
 *   1. §16 the scan-error banner renders whenever `scanState.error` is
 *      non-empty, and is absent when it is empty.
 *   2. The review-stage header never claims "Scan complete" while
 *      `scanState.error` is set (a failed/timed-out scan showing a success
 *      label right next to the error banner is self-contradictory).
 *
 * Non-vacuous: delete/misbind the `scanState.error` `v-if` on the banner, or
 * drop the error branch from the header label, and the matching assertion
 * fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ScanLibrary from './ScanLibrary.vue'
import type { ScanState } from './scanLibrary.types'

function baseScanState(overrides: Partial<ScanState> = {}): ScanState {
  return { status: 'done', processed: 10, total: 10, error: '', ...overrides }
}

describe('ScanLibrary', () => {
  it('renders the scan-error banner when scanState.error is non-empty', () => {
    const wrapper = mount(ScanLibrary, {
      props: {
        scanState: baseScanState({ error: 'scan timed out after 30m0s' }),
        entries: [],
      },
    })

    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('scan timed out after 30m0s')
  })

  it('renders no error banner when scanState.error is empty', () => {
    const wrapper = mount(ScanLibrary, {
      props: {
        scanState: baseScanState(),
        entries: [],
      },
    })

    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('review header shows the success label when the scan finished without error', () => {
    const wrapper = mount(ScanLibrary, {
      props: {
        scanState: baseScanState(),
        entries: [],
      },
    })

    expect(wrapper.find('.sl-review-head__done').text()).toBe('Scan complete · 10 found')
  })

  it('review header does NOT claim "Scan complete" when the scan errored', () => {
    const wrapper = mount(ScanLibrary, {
      props: {
        scanState: baseScanState({ error: 'scan timed out after 30m0s' }),
        entries: [],
      },
    })

    expect(wrapper.find('.sl-review-head__done').text()).not.toContain('Scan complete')
  })
})
