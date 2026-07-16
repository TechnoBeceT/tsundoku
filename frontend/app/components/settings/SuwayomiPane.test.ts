/**
 * SuwayomiPane — toggle + dirty/Save wiring for the sole remaining card
 * (Tsundoku-owned FlareSolverr, QCAT-238). The proxied Suwayomi SOCKS card
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
import type { FlareSolverrConfig } from '../screens/settings.types'

const baseFlareSolverr: FlareSolverrConfig = {
  enabled: false,
  url: '',
  timeout: { value: 60, unit: 's' },
  session: '',
  sessionTtl: { value: 15, unit: 'm' },
  fallback: false,
}

function saveButton(wrapper: ReturnType<typeof mount>) {
  return wrapper.find('button[type="submit"]')
}

describe('SuwayomiPane', () => {
  it('enabling FlareSolverr + entering a URL flips the Save button and emits the merged config', async () => {
    const wrapper = mount(SuwayomiPane, {
      props: { flareSolverr: baseFlareSolverr },
    })

    // Starts clean: nothing edited yet, Save disabled.
    expect(saveButton(wrapper).attributes('disabled')).toBeDefined()

    // Flip the FlareSolverr toggle on — a whole-object v-model update.
    const flareToggle = wrapper.find('[aria-label="Enable FlareSolverr"]')
    await flareToggle.trigger('click')

    // The URL field only renders once `modelValue.enabled` is true — its very
    // presence proves the toggle's `update:model-value` actually reached the
    // local `flare` copy (under the const-reactive bug it never does).
    const urlField = wrapper.find('.flare-body .field__input')
    expect(urlField.exists()).toBe(true)
    await urlField.setValue('http://flaresolverr:8191')

    expect(saveButton(wrapper).attributes('disabled')).toBeUndefined()

    await saveButton(wrapper).trigger('click')

    // §16: the emitted payload carries the full merged FlareSolverr config.
    const emitted = wrapper.emitted('save-flaresolverr')
    expect(emitted).toBeTruthy()
    const saved = emitted![0]![0] as FlareSolverrConfig
    expect(saved.enabled).toBe(true)
    expect(saved.url).toBe('http://flaresolverr:8191')
  })
})
