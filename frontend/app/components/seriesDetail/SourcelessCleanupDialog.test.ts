/**
 * SourcelessCleanupDialog — mirrors FractionalCleanupDialog.test.ts, minus the
 * page-count-yardstick assertions (there is no full-size-vs-notice ambiguity
 * here: every listed chapter is orphaned by definition, so all rows start
 * pre-ticked and the owner opts OUT rather than in).
 *
 * QCAT-222 (owner-ratified, NON-NEGOTIABLE): this delete has no in-product
 * inverse — it permanently deletes CBZ files — so it must fire ONLY through
 * the shared `ConfirmModal` (destructive). The tests below prove the wiring,
 * not just the copy: clicking the list's own "Delete N files" trigger must
 * NOT emit `confirm` by itself; only confirming the nested `ConfirmModal`
 * (its own "Delete files" button) does.
 *
 * The real Dialog teleports through reka-ui's portal (which does not render in
 * happy-dom), so it is stubbed to render its title + slots inline — the same
 * approach as FractionalCleanupDialog.test.ts. `ConfirmModal` itself is NOT
 * stubbed (only the `Dialog` it wraps internally is, via the same global
 * stub), so its real title/message/confirm-label all render for real.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SourcelessCleanupDialog from './SourcelessCleanupDialog.vue'
import type { SourcelessCleanupPreview } from '../screens/sourceless.types'

const DialogStub = {
  props: ['open', 'title'],
  template: '<div v-if="open" class="dialog-stub"><h2>{{ title }}</h2><slot /><slot name="actions" /></div>',
}

const preview: SourcelessCleanupPreview = {
  chapters: [
    { chapterId: 's-067', number: 67, pageCount: 42, provider: '', filename: '[KaliScan][en] Solo Leveling 067.cbz' },
    { chapterId: 's-070', number: 70, pageCount: 38, provider: '', filename: '[KaliScan][en] Solo Leveling 070.cbz' },
    { chapterId: 's-073', number: 73, pageCount: null, provider: '', filename: '[KaliScan][en] Solo Leveling 073.cbz' },
  ],
}

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(SourcelessCleanupDialog, {
    props: { open: true, seriesTitle: 'Solo Leveling', preview, busy: false, error: null, ...props },
    global: { stubs: { Dialog: DialogStub } },
  })
}

/** Only the per-ROW checkboxes (excludes the "select all" affordance). */
function rowCheckboxes(wrapper: ReturnType<typeof mountDialog>) {
  return wrapper.findAll('.src-row [role="checkbox"]')
}

function tickState(wrapper: ReturnType<typeof mountDialog>): boolean[] {
  return rowCheckboxes(wrapper).map((box) => box.attributes('aria-checked') === 'true')
}

function triggerButton(wrapper: ReturnType<typeof mountDialog>) {
  return wrapper.findAll('button').find((b) => b.text().startsWith('Delete ') && b.text().includes('file'))!
}

function confirmModalButton(wrapper: ReturnType<typeof mountDialog>) {
  return wrapper.findAll('button').find((b) => b.text() === 'Delete files')!
}

describe('SourcelessCleanupDialog — selection', () => {
  it('pre-ticks every removable chapter (nothing here is ambiguous, unlike Fractionals)', () => {
    expect(tickState(mountDialog())).toEqual([true, true, true])
  })

  it('unticking a row updates the trigger button count', async () => {
    const wrapper = mountDialog()
    expect(triggerButton(wrapper).text()).toBe('Delete 3 files')

    await rowCheckboxes(wrapper)[1]!.trigger('click')

    expect(triggerButton(wrapper).text()).toBe('Delete 2 files')
    expect(tickState(wrapper)).toEqual([true, false, true])
  })

  it('the "select all" affordance toggles every row at once', async () => {
    const wrapper = mountDialog()
    const selectAll = wrapper.find('.src__selectall [role="checkbox"]')

    await selectAll.trigger('click')
    expect(tickState(wrapper)).toEqual([false, false, false])
    expect(triggerButton(wrapper).text()).toBe('Delete 0 files')
    expect(triggerButton(wrapper).attributes('disabled')).toBeDefined()

    await selectAll.trigger('click')
    expect(tickState(wrapper)).toEqual([true, true, true])
  })

  it('shows the chapter number, page count, filename and former source columns', () => {
    const text = mountDialog().text()

    expect(text).toContain('67')
    expect(text).toContain('42p')
    expect(text).toContain('[KaliScan][en] Solo Leveling 067.cbz')
    // provider is '' (the whole point of "sourceless") — the former source
    // column must never render a blank cell, it renders the em-dash.
    expect(mountDialog().findAll('.src-row')[0]!.text()).toContain('—')
  })

  it('renders an em-dash for a chapter with no recorded page count', () => {
    expect(mountDialog().findAll('.src-row')[2]!.text()).toContain('—')
  })
})

describe('SourcelessCleanupDialog — the QCAT-222 ConfirmModal gate', () => {
  it('renders the explainer copy about permanent deletion', () => {
    expect(mountDialog().text()).toContain('carried by no source')
  })

  it('clicking the list\'s own trigger does NOT delete anything by itself', async () => {
    const wrapper = mountDialog()

    await triggerButton(wrapper).trigger('click')

    // The destructive action has not fired — only the confirm gate opened.
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })

  it('opens the shared ConfirmModal with the mandated danger copy and "Delete files" label', async () => {
    const wrapper = mountDialog()

    await triggerButton(wrapper).trigger('click')

    expect(wrapper.text()).toContain('No source can restore these chapters. The CBZ files will be permanently deleted.')
    expect(confirmModalButton(wrapper)).toBeDefined()
  })

  it('emits confirm with the ticked ids ONLY after the ConfirmModal is itself confirmed', async () => {
    const wrapper = mountDialog()

    await rowCheckboxes(wrapper)[1]!.trigger('click') // untick the 70
    await triggerButton(wrapper).trigger('click')
    await confirmModalButton(wrapper).trigger('click')

    expect(wrapper.emitted('confirm')).toHaveLength(1)
    expect(wrapper.emitted('confirm')![0]).toEqual([['s-067', 's-073']])
  })

  it('never emits when nothing is ticked (the trigger disables at zero)', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.src__selectall [role="checkbox"]').trigger('click')

    expect(triggerButton(wrapper).attributes('disabled')).toBeDefined()
  })
})

describe('SourcelessCleanupDialog — busy/error/empty states', () => {
  it('shows a failed removal\'s reason inside the dialog (§16 — it stays open)', () => {
    expect(mountDialog({ error: 'Update failed' }).text()).toContain('Update failed')
  })

  it('disables the trigger button while busy', () => {
    expect(triggerButton(mountDialog({ busy: true })).attributes('disabled')).toBeDefined()
  })

  it('renders nothing removable when the preview has an empty chapter list', () => {
    const wrapper = mountDialog({ preview: { chapters: [] } })

    expect(wrapper.findAll('.src-row')).toHaveLength(0)
    expect(wrapper.find('.src__selectall').exists()).toBe(false)
    expect(triggerButton(wrapper).text()).toBe('Delete 0 files')
    expect(triggerButton(wrapper).attributes('disabled')).toBeDefined()
  })

  it('renders nothing removable when the preview is null (still loading)', () => {
    const wrapper = mountDialog({ preview: null })

    expect(wrapper.findAll('.src-row')).toHaveLength(0)
  })

  it('re-seeds the pre-ticked selection on every re-open', async () => {
    const wrapper = mountDialog()
    await rowCheckboxes(wrapper)[0]!.trigger('click')
    expect(tickState(wrapper)[0]).toBe(false)

    await wrapper.setProps({ open: false })
    await wrapper.setProps({ open: true })

    expect(tickState(wrapper)).toEqual([true, true, true])
  })

  it('emits close when Cancel is pressed', async () => {
    const wrapper = mountDialog()

    await wrapper.findAll('button').find((b) => b.text() === 'Cancel')!.trigger('click')

    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})
