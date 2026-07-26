/**
 * LibraryDedupDialog — the QCAT-222 gate in front of the library-wide
 * duplicate-source clean-up.
 *
 * The sweep renames CBZ files across the entire library and deletes the drained
 * duplicate source rows, so the trigger button must NOT start it directly: it may
 * only open the shared destructive `ConfirmModal`, whose own confirm button is the
 * one and only thing that emits `confirm`. These tests pin the wiring, not the
 * copy — clicking the trigger must emit nothing.
 *
 * The real `Dialog` teleports through reka-ui's portal (which does not render in
 * happy-dom), so it is stubbed to render its title + slots inline — the same
 * approach as FractionalCleanupDialog.test.ts. `ConfirmModal` itself is NOT
 * stubbed, so its real title / confirm-label / destructive treatment render.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import LibraryDedupDialog from './LibraryDedupDialog.vue'

const DialogStub = {
  props: ['open', 'title'],
  template: '<div v-if="open" class="dialog-stub"><h2>{{ title }}</h2><slot /><slot name="actions" /></div>',
}

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(LibraryDedupDialog, {
    props,
    global: { stubs: { Dialog: DialogStub } },
  })
}

/**
 * The card's own trigger — opens the confirm gate, starts nothing by itself.
 * Matched on either label because it reads "Starting…" while busy.
 */
function trigger(wrapper: ReturnType<typeof mountDialog>) {
  return wrapper.findAll('button').find(
    (b) => b.text().includes('Clean up duplicate sources') || b.text() === 'Starting…',
  )!
}

/** The nested destructive ConfirmModal's own button — the ONLY thing that emits. */
function confirmButton(wrapper: ReturnType<typeof mountDialog>) {
  return wrapper.findAll('button').find((b) => b.text() === 'Clean up library')
}

describe('LibraryDedupDialog', () => {
  it('renders the trigger and no confirm gate until it is clicked', () => {
    const wrapper = mountDialog()
    expect(trigger(wrapper)).toBeTruthy()
    expect(confirmButton(wrapper)).toBeUndefined()
  })

  it('the trigger opens the shared destructive ConfirmModal and emits NOTHING by itself', async () => {
    const wrapper = mountDialog()
    await trigger(wrapper).trigger('click')

    expect(wrapper.emitted('confirm')).toBeUndefined()
    expect(confirmButton(wrapper)).toBeTruthy()
    // The copy must be honest about what happens to the files.
    expect(wrapper.text()).toContain('RENAMED')
    expect(wrapper.text()).toContain('KEPT')
  })

  it('only the ConfirmModal confirm button emits confirm', async () => {
    const wrapper = mountDialog()
    await trigger(wrapper).trigger('click')
    await confirmButton(wrapper)!.trigger('click')

    expect(wrapper.emitted('confirm')).toHaveLength(1)
  })

  it('a busy trigger cannot open the gate (§16 in-flight)', async () => {
    const wrapper = mountDialog({ busy: true })
    await trigger(wrapper).trigger('click')

    expect(confirmButton(wrapper)).toBeUndefined()
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })

  it('closes the gate once the request finishes so the outcome line is readable (§16)', async () => {
    const wrapper = mountDialog()
    await trigger(wrapper).trigger('click')
    expect(confirmButton(wrapper)).toBeTruthy()

    await wrapper.setProps({ busy: true })
    await wrapper.setProps({ busy: false, message: 'Dedup started' })

    expect(confirmButton(wrapper)).toBeUndefined()
    expect(wrapper.text()).toContain('Dedup started')
  })

  it('surfaces a failure instead of swallowing it (§16)', () => {
    const wrapper = mountDialog({ error: 'Dedup failed — engine unreachable' })
    expect(wrapper.text()).toContain('Dedup failed — engine unreachable')
  })
})
