/**
 * SuwayomiPane — toggle + dirty/Save wiring for its two cards (Tsundoku-owned
 * FlareSolverr, QCAT-238, and the impersonate gateway, GAP-111/GAP-131). The
 * proxied Suwayomi SOCKS card
 * was RETIRED with the P2 Suwayomi-removal backend cutover — do not re-add
 * a second card/composable here.
 *
 * Pins the regression this file originally caught: `flare` is `reactive(...)`
 * bound with whole-object `v-model`, which desugars to `flare = $event` —
 * reassigning a `const` throws `TypeError: Assignment to constant variable`.
 * Vue swallows the throw, so the local copy never updates, `dirty` never
 * flips, and the Save button stays disabled forever. This test starts with
 * FlareSolverr DISABLED (the shipped fixture already had it enabled, which is
 * why no test caught this), flips the toggle on, types a URL, and asserts the
 * Save button actually reacts.
 *
 * Non-vacuous: against the pre-fix `const` binding this throws inside the
 * component and the Save button's `disabled` attribute stays present; after
 * the fix the toggle mutates the local copy in place, `dirty` flips true, and
 * the button's `disabled` attribute is removed.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SuwayomiPane from './SuwayomiPane.vue'
import type { FlareSolverrConfig, ImpersonateConfig, SourceOption } from '../screens/settings.types'

const baseFlareSolverr: FlareSolverrConfig = {
  enabled: false,
  url: '',
  timeout: { value: 60, unit: 's' },
  session: '',
  sessionTtl: { value: 15, unit: 'm' },
  fallback: false,
}

const baseImpersonate: ImpersonateConfig = {
  enabled: false,
  url: '',
  sourceIds: [],
}

const impersonateSources: SourceOption[] = [
  { id: '1998416842837112832', name: 'Hive Scans', lang: 'en' },
  { id: '42', name: 'Comix', lang: 'en' },
]

function mountPane(
  overrides: Partial<{
    flareSolverr: FlareSolverrConfig
    impersonate: ImpersonateConfig
    impersonateSources: SourceOption[]
  }> = {},
) {
  return mount(SuwayomiPane, {
    props: {
      flareSolverr: baseFlareSolverr,
      impersonate: baseImpersonate,
      impersonateSources,
      ...overrides,
    },
  })
}

describe('SuwayomiPane', () => {
  it('enabling FlareSolverr + entering a URL flips the Save button and emits the merged config', async () => {
    const wrapper = mountPane()

    // The FlareSolverr card is the first stacked card; its Save button is the
    // first submit button. Starts clean: nothing edited yet, Save disabled.
    const flareSaveBtn = () => wrapper.findAll('button[type="submit"]')[0]!
    expect(flareSaveBtn().attributes('disabled')).toBeDefined()

    // Flip the FlareSolverr toggle on — a whole-object v-model update.
    const flareToggle = wrapper.find('[aria-label="Enable FlareSolverr"]')
    await flareToggle.trigger('click')

    // The URL field only renders once `modelValue.enabled` is true — its very
    // presence proves the toggle's `update:model-value` actually reached the
    // local `flare` copy (under the const-reactive bug it never does).
    const urlField = wrapper.find('.flare-body .field__input')
    expect(urlField.exists()).toBe(true)
    await urlField.setValue('http://flaresolverr:8191')

    expect(flareSaveBtn().attributes('disabled')).toBeUndefined()

    await flareSaveBtn().trigger('click')

    // §16: the emitted payload carries the full merged FlareSolverr config.
    const emitted = wrapper.emitted('save-flaresolverr')
    expect(emitted).toBeTruthy()
    const saved = emitted![0]![0] as FlareSolverrConfig
    expect(saved.enabled).toBe(true)
    expect(saved.url).toBe('http://flaresolverr:8191')
  })

  it('enabling the impersonate gateway + entering a URL flips its Save button and emits the merged config', async () => {
    const wrapper = mountPane()

    // The impersonate card is the second stacked card; its Save button is the
    // second submit button.
    const impSaveBtn = () => wrapper.findAll('button[type="submit"]')[1]!
    expect(impSaveBtn().attributes('disabled')).toBeDefined()

    // Flip the impersonate toggle on — a whole-object v-model update (the same
    // const-reactive regression guard as the FlareSolverr card above).
    await wrapper.find('[aria-label="Enable impersonate gateway"]').trigger('click')

    const urlField = wrapper.find('.imp-body .field__input')
    expect(urlField.exists()).toBe(true)
    await urlField.setValue('http://impersonate-gateway:8788')

    expect(impSaveBtn().attributes('disabled')).toBeUndefined()

    await impSaveBtn().trigger('click')

    // §16: the emitted payload carries the full merged impersonate config.
    const emitted = wrapper.emitted('save-impersonate')
    expect(emitted).toBeTruthy()
    const saved = emitted![0]![0] as ImpersonateConfig
    expect(saved.enabled).toBe(true)
    expect(saved.url).toBe('http://impersonate-gateway:8788')
  })

  it('ticking a source flips the impersonate Save button and emits that source id (GAP-131)', async () => {
    const wrapper = mountPane({
      impersonate: { enabled: true, url: 'http://impersonate-gateway:8788', sourceIds: [] },
    })

    const impSaveBtn = () => wrapper.findAll('button[type="submit"]')[1]!
    expect(impSaveBtn().attributes('disabled')).toBeDefined()

    // Non-vacuous: the tick has to travel card → local copy → dirty → Save. Drop
    // the card's `sourceIds` out of the emitted patch (or bind the picker to the
    // prop instead of the local copy) and the Save button below stays disabled.
    await wrapper.find('[aria-label="Use the image proxy for Hive Scans"]').trigger('click')

    expect(impSaveBtn().attributes('disabled')).toBeUndefined()

    await impSaveBtn().trigger('click')

    const saved = wrapper.emitted('save-impersonate')![0]![0] as ImpersonateConfig
    // The ID is what travels, never the display name.
    expect(saved.sourceIds).toEqual(['1998416842837112832'])
  })

  it('unticking the last source emits an EMPTY set rather than dropping the field', async () => {
    const wrapper = mountPane({
      impersonate: { enabled: true, url: 'http://gw:8788', sourceIds: ['42'] },
    })

    await wrapper.find('[aria-label="Use the image proxy for Comix"]').trigger('click')
    await wrapper.findAll('button[type="submit"]')[1]!.trigger('click')

    const saved = wrapper.emitted('save-impersonate')![0]![0] as ImpersonateConfig
    expect(saved.sourceIds).toEqual([])
  })
})
