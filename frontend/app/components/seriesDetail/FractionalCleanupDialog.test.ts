/**
 * FractionalCleanupDialog — the guarantees that stand between the owner and 267
 * pages of destroyed content.
 *
 * The fixture IS the owner's live prod case ("A Returner's Magic Should Be
 * Special", typical whole chapter 96p): 181.5/190.5 are ONE-PAGE notices, 3.1 is
 * 5p, 224.5 is 16p — but 221.5 and 223.5 are 132p and 135p, full-size chapters
 * that merely carry a ".5" number. Pinned here:
 *   1. the pre-tick rule, asserted PER ROW (the two full-size chapters are
 *      pre-UNTICKED + flagged; the four junk files are pre-ticked);
 *   2. the confirm label states the REAL count and is disabled at zero;
 *   3. THE ONE THAT MATTERS: confirming emits ONLY the TICKED ids — sending all
 *      of them would delete exactly the chapters the owner deliberately unticked.
 *
 * The real Dialog teleports through reka-ui's portal (which does not render in
 * happy-dom), so it is stubbed to render its title + slots inline — the same
 * approach as RemoveSourceDialog.test.ts. The Checkbox atom is NOT stubbed: its
 * `aria-checked` is what makes the per-row assertions non-vacuous.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import FractionalCleanupDialog from './FractionalCleanupDialog.vue'
import type { FractionalCleanupChapter } from '../screens/seriesDetail.types'

const DialogStub = {
  props: ['open', 'title'],
  template: '<div v-if="open" class="dialog-stub"><h2>{{ title }}</h2><slot /><slot name="actions" /></div>',
}

/** The owner's real removable set (live prod). */
const ownersRealCase: FractionalCleanupChapter[] = [
  { chapterId: 'c-1815', number: 181.5, pageCount: 1, provider: 'KaliScan', filename: 'a.cbz' },
  { chapterId: 'c-1905', number: 190.5, pageCount: 1, provider: 'KaliScan', filename: 'b.cbz' },
  { chapterId: 'c-31', number: 3.1, pageCount: 5, provider: 'Comic Asura', filename: 'c.cbz' },
  { chapterId: 'c-2245', number: 224.5, pageCount: 16, provider: 'KaliScan', filename: 'd.cbz' },
  { chapterId: 'c-2215', number: 221.5, pageCount: 132, provider: 'KaliScan', filename: 'e.cbz' },
  { chapterId: 'c-2235', number: 223.5, pageCount: 135, provider: 'KaliScan', filename: 'f.cbz' },
]

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(FractionalCleanupDialog, {
    props: { open: true, chapters: ownersRealCase, typicalPageCount: 96, ...props },
    global: { stubs: { Dialog: DialogStub } },
  })
}

/** The checkbox tick state, in row order (reka renders `role="checkbox"` + aria-checked). */
function tickState(wrapper: ReturnType<typeof mountDialog>): boolean[] {
  return wrapper.findAll('[role="checkbox"]').map((box) => box.attributes('aria-checked') === 'true')
}

function confirmButton(wrapper: ReturnType<typeof mountDialog>) {
  return wrapper.findAll('button').find((b) => b.text().startsWith('Remove '))!
}

describe('FractionalCleanupDialog — the pre-tick rule (advisory only)', () => {
  it('pre-ticks the junk and pre-UNTICKS the two full-size chapters (the owner’s real case)', () => {
    const wrapper = mountDialog()

    // 1p, 1p, 5p, 16p ticked · 132p, 135p (>= 48p = half of 96) NOT ticked.
    expect(tickState(wrapper)).toEqual([true, true, true, true, false, false])
  })

  it('flags ONLY the full-size chapters with the ⚠ warning', () => {
    const rows = mountDialog().findAll('.frac-row')

    expect(rows.filter((r) => r.text().includes('⚠ full-size chapter'))).toHaveLength(2)
    expect(rows[4]!.text()).toContain('⚠ full-size chapter')
    expect(rows[5]!.text()).toContain('⚠ full-size chapter')
    expect(rows[0]!.text()).not.toContain('⚠')
  })

  it('shows the page count and the yardstick — the evidence the owner judges from', () => {
    const wrapper = mountDialog()

    expect(wrapper.text()).toContain('typical chapter: 96p')
    expect(wrapper.findAll('.frac-row')[4]!.text()).toContain('132p')
  })

  it('pre-ticks everything when every entry is junk', () => {
    const wrapper = mountDialog({
      chapters: [
        { chapterId: 'j-1', number: 12.5, pageCount: 1, provider: 'KaliScan', filename: 'x.cbz' },
        { chapterId: 'j-2', number: 77.1, pageCount: 5, provider: 'KaliScan', filename: 'y.cbz' },
      ],
    })

    expect(tickState(wrapper)).toEqual([true, true])
    expect(confirmButton(wrapper).text()).toBe('Remove 2 files')
  })

  it('pre-ticks NOTHING when every entry is full-size — and the confirm button is disabled', () => {
    const wrapper = mountDialog({
      chapters: [
        { chapterId: 'f-1', number: 221.5, pageCount: 132, provider: 'KaliScan', filename: 'e.cbz' },
        { chapterId: 'f-2', number: 223.5, pageCount: 135, provider: 'KaliScan', filename: 'f.cbz' },
      ],
    })

    expect(tickState(wrapper)).toEqual([false, false])
    expect(confirmButton(wrapper).text()).toBe('Remove 0 files')
    expect(confirmButton(wrapper).attributes('disabled')).toBeDefined()
  })

  it('pre-ticks nothing and flags nothing when there is no yardstick (typicalPageCount = 0)', () => {
    const wrapper = mountDialog({
      typicalPageCount: 0,
      chapters: [
        { chapterId: 'n-1', number: 5.5, pageCount: 18, provider: 'KaliScan', filename: 'x.cbz' },
        { chapterId: 'n-2', number: 9.1, pageCount: null, provider: '', filename: 'y.cbz' },
      ],
    })

    expect(tickState(wrapper)).toEqual([false, false])
    expect(wrapper.text()).not.toContain('⚠')
    expect(wrapper.text()).not.toContain('typical chapter')
    // Unmeasured page counts render as an em-dash, never a fake "0p".
    expect(wrapper.findAll('.frac-row')[1]!.text()).toContain('—')
  })
})

describe('FractionalCleanupDialog — the confirm', () => {
  it('the button count tracks the checkboxes live, and disables at zero', async () => {
    const wrapper = mountDialog()
    expect(confirmButton(wrapper).text()).toBe('Remove 4 files')

    // Owner unticks the 5p one → 3.
    await wrapper.findAll('[role="checkbox"]')[2]!.trigger('click')
    expect(confirmButton(wrapper).text()).toBe('Remove 3 files')

    // Owner ticks the 132p full-size chapter anyway (the rule is advisory) → 4.
    await wrapper.findAll('[role="checkbox"]')[4]!.trigger('click')
    expect(confirmButton(wrapper).text()).toBe('Remove 4 files')
    expect(confirmButton(wrapper).attributes('disabled')).toBeUndefined()

    // Untick them all → 0, disabled.
    for (const i of [0, 1, 3, 4]) await wrapper.findAll('[role="checkbox"]')[i]!.trigger('click')
    expect(confirmButton(wrapper).text()).toBe('Remove 0 files')
    expect(confirmButton(wrapper).attributes('disabled')).toBeDefined()
  })

  it('emits ONLY the TICKED ids — never the whole preview', async () => {
    const wrapper = mountDialog()

    await confirmButton(wrapper).trigger('click')

    // The four pre-ticked junk files — NOT the two full-size chapters the owner left unticked.
    expect(wrapper.emitted('confirm')).toHaveLength(1)
    expect(wrapper.emitted('confirm')![0]).toEqual([['c-1815', 'c-1905', 'c-31', 'c-2245']])
  })

  it('emits the owner’s edited selection, not the pre-tick default', async () => {
    const wrapper = mountDialog()

    // Untick both 1-page notices, tick the 132p chapter.
    await wrapper.findAll('[role="checkbox"]')[0]!.trigger('click')
    await wrapper.findAll('[role="checkbox"]')[1]!.trigger('click')
    await wrapper.findAll('[role="checkbox"]')[4]!.trigger('click')
    await confirmButton(wrapper).trigger('click')

    expect(wrapper.emitted('confirm')![0]).toEqual([['c-31', 'c-2245', 'c-2215']])
  })

  it('never emits when nothing is ticked', async () => {
    const wrapper = mountDialog({ typicalPageCount: 0 })

    await confirmButton(wrapper).trigger('click')

    expect(wrapper.emitted('confirm')).toBeUndefined()
  })

  it('shows a failed removal’s reason inside the dialog (§16 — it stays open)', () => {
    expect(mountDialog({ error: 'Update failed' }).text()).toContain('Update failed')
  })

  it('re-seeds the pre-tick state on every re-open (a re-open never inherits stale ticks)', async () => {
    const wrapper = mountDialog()
    await wrapper.findAll('[role="checkbox"]')[0]!.trigger('click')
    expect(tickState(wrapper)[0]).toBe(false)

    await wrapper.setProps({ open: false })
    await wrapper.setProps({ open: true })

    expect(tickState(wrapper)).toEqual([true, true, true, true, false, false])
  })
})
