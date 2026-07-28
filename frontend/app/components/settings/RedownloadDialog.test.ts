/**
 * RedownloadDialog — the QCAT-222 gate in front of the library-wide bulk
 * re-download, plus the two pieces of filter semantics the UI is solely
 * responsible for getting right.
 *
 * 1. The sweep re-queues chapters across the whole library, so the "Re-download
 *    N chapters" button must NOT start it: it may only open the shared
 *    destructive `ConfirmModal`, whose own confirm button is the one and only
 *    thing that emits `confirm`.
 * 2. The scanlator filter is PRESENCE-based, not emptiness-based. Unticked emits
 *    `scanlator: null` (every scanlator of the source); ticked-but-blank emits
 *    `scanlator: ''` (the source's all-scanlators provider specifically). A bare
 *    text field cannot express both, and getting it wrong silently changes which
 *    chapters are swept.
 * 3. The cutoff is typed in LOCAL time and must leave as a UTC RFC 3339 instant.
 *
 * The real `Dialog` teleports through reka-ui's portal (which does not render in
 * happy-dom), so it is stubbed — the same approach as LibraryDedupDialog.test.ts.
 * `ConfirmModal` itself is NOT stubbed, so its real confirm button renders.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import RedownloadDialog from './RedownloadDialog.vue'
import type { RedownloadFilter } from '~/composables/useRedownload'

const DialogStub = {
  props: ['open', 'title'],
  template: '<div v-if="open" class="dialog-stub"><h2>{{ title }}</h2><slot /><slot name="actions" /></div>',
}

const preview = { matched: 231, perCycle: 10, estimatedCycles: 24 }

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(RedownloadDialog, { props, global: { stubs: { Dialog: DialogStub } } })
}

type Wrapper = ReturnType<typeof mountDialog>

/** Fills the two mandatory fields (and optionally the scanlator narrowing). */
async function fillFilter(w: Wrapper, opts: { scanlator?: string } = {}): Promise<void> {
  const inputs = w.findAll('input')
  await inputs.find((i) => i.attributes('type') !== 'datetime-local' && i.attributes('role') !== 'checkbox')!
    .setValue('Comix')
  if (opts.scanlator !== undefined) {
    await w.find('[role="checkbox"]').trigger('click')
    const scanlatorInput = w.findAll('input').filter((i) => i.attributes('type') !== 'datetime-local')[1]!
    await scanlatorInput.setValue(opts.scanlator)
  }
  await w.findAll('input').find((i) => i.attributes('type') === 'datetime-local')!
    .setValue('2026-07-25T10:39')
}

/** The "Check" (preview) button. */
const checkButton = (w: Wrapper) =>
  w.findAll('button').find((b) => b.text() === 'Check' || b.text() === 'Checking…')!

/** The card's own apply trigger — opens the confirm gate, starts nothing itself. */
const applyTrigger = (w: Wrapper) =>
  w.findAll('button').find((b) => b.text().startsWith('Re-download 231'))

/** The nested destructive ConfirmModal's own button — the ONLY thing that emits. */
const confirmButton = (w: Wrapper) =>
  w.findAll('button').find((b) => b.text() === 'Re-download' && !b.text().includes('231'))

describe('RedownloadDialog — the QCAT-222 confirm gate', () => {
  it('does not offer the apply trigger until a preview is loaded', async () => {
    const w = mountDialog()
    await fillFilter(w)

    expect(applyTrigger(w)).toBeUndefined()
  })

  it('the apply trigger opens the ConfirmModal and emits NOTHING by itself', async () => {
    const w = mountDialog({ preview })
    await fillFilter(w)
    await applyTrigger(w)!.trigger('click')

    expect(w.emitted('confirm')).toBeUndefined()
    expect(confirmButton(w)).toBeTruthy()
  })

  it('the confirm copy states that nothing is deleted, and quotes the cycle cost', async () => {
    const w = mountDialog({ preview })
    await fillFilter(w)
    await applyTrigger(w)!.trigger('click')

    const text = w.text()
    expect(text).toContain('Nothing is deleted')
    expect(text).toContain('24 download cycles')
    expect(text).toContain('10 chapters per cycle')
  })

  it('only the ConfirmModal\'s own button emits `confirm`', async () => {
    const w = mountDialog({ preview })
    await fillFilter(w)
    await applyTrigger(w)!.trigger('click')
    await confirmButton(w)!.trigger('click')

    expect(w.emitted('confirm')).toHaveLength(1)
  })

  /**
   * The filter is not idempotent across runs: it selects on when the CBZ was last
   * WRITTEN, and a successful re-download rewrites that to now, so this sweep's own
   * output still matches the same filter afterwards. Re-running it unchanged pays
   * for every chapter twice. This copy is the only warning the owner gets.
   */
  it('warns that re-running the same filter re-queues the chapters it just fixed', () => {
    const text = mountDialog().text()

    expect(text).toContain('updates that time to now')
    expect(text).toContain('re-queues the chapters it already fixed')
  })

  it('disables the apply trigger when the preview matched nothing', async () => {
    const w = mountDialog({ preview: { matched: 0, perCycle: 10, estimatedCycles: 0 } })
    await fillFilter(w)

    expect(w.findAll('button').find((b) => b.text().startsWith('Re-download 0'))!.attributes('disabled')).toBeDefined()
  })
})

describe('RedownloadDialog — filter semantics', () => {
  it('emits `preview` only once both mandatory fields are set', async () => {
    const w = mountDialog()
    expect(checkButton(w).attributes('disabled')).toBeDefined()

    await fillFilter(w)
    expect(checkButton(w).attributes('disabled')).toBeUndefined()

    await checkButton(w).trigger('click')
    expect(w.emitted('preview')).toHaveLength(1)
  })

  it('sends scanlator: null when the owner is NOT narrowing (every scanlator of the source)', async () => {
    const w = mountDialog()
    await fillFilter(w)
    await checkButton(w).trigger('click')

    const filter = w.emitted('preview')![0]![0] as RedownloadFilter
    expect(filter.source).toBe('Comix')
    expect(filter.scanlator).toBeNull()
  })

  it('sends scanlator: "" when narrowing is ticked but left blank (the all-scanlators provider itself)', async () => {
    const w = mountDialog()
    await fillFilter(w, { scanlator: '' })
    await checkButton(w).trigger('click')

    const filter = w.emitted('preview')!.at(-1)![0] as RedownloadFilter
    expect(filter.scanlator).toBe('')
  })

  it('sends the named scanlator when one is typed', async () => {
    const w = mountDialog()
    await fillFilter(w, { scanlator: 'Valir Scans' })
    await checkButton(w).trigger('click')

    const filter = w.emitted('preview')!.at(-1)![0] as RedownloadFilter
    expect(filter.scanlator).toBe('Valir Scans')
  })

  it('converts the locally-typed cutoff into a UTC RFC 3339 instant', async () => {
    const w = mountDialog()
    await fillFilter(w)
    await checkButton(w).trigger('click')

    const filter = w.emitted('preview')![0]![0] as RedownloadFilter
    expect(filter.since).toBe(new Date('2026-07-25T10:39').toISOString())
    expect(filter.since).toMatch(/Z$/)
  })

  it('emits `reset` when the filter changes, so a stale count is never shown beside a new filter', async () => {
    const w = mountDialog({ preview })
    await fillFilter(w)

    expect(w.emitted('reset')?.length).toBeGreaterThan(0)
  })
})
