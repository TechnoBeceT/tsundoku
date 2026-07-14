/**
 * SuwayomiPane — toggle + dirty/Save wiring, per-card independence (QCAT-238).
 *
 * Since FlareSolverr moved to its own Tsundoku-owned endpoint, the pane now
 * has TWO independent cards, each with its own local reactive copy, dirty
 * computed, and SaveFooter. This pins two things:
 *
 * 1. The regression this file originally caught: `socks`/`flare` were
 *    `reactive(...)` bound with whole-object `v-model`, which desugars to
 *    `socks = $event` — reassigning a `const` throws `TypeError: Assignment
 *    to constant variable`. Vue swallows the throw, so the local copy never
 *    updates, `dirty` never flips, and the Save button stays disabled
 *    forever. This test starts with FlareSolverr DISABLED (the shipped
 *    fixture already had it enabled, which is why no test caught this),
 *    flips the toggle on, types a URL, and asserts the FlareSolverr Save
 *    button actually reacts — independently of the SOCKS Save button.
 * 2. The two cards are independent: editing one never enables the other's
 *    Save button, and each Save button emits only its OWN config shape.
 *
 * Non-vacuous: against the pre-fix `const` binding this throws inside the
 * component and the relevant Save button's `disabled` attribute stays
 * present; after the fix the toggle mutates the local copy in place, `dirty`
 * flips true, and the button's `disabled` attribute is removed.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SuwayomiPane from './SuwayomiPane.vue'
import type { FlareSolverrConfig, SuwayomiConfig } from '../screens/settings.types'

const baseConfig: SuwayomiConfig = {
  database: {
    type: 'PostgreSQL',
    url: 'jdbc:postgresql://db:5432/suwayomi',
    username: 'suwayomi',
  },
  socks: {
    enabled: false,
    version: '5',
    host: '',
    port: '1080',
    username: '',
    password: '',
  },
}

const baseFlareSolverr: FlareSolverrConfig = {
  enabled: false,
  url: '',
  timeout: { value: 60, unit: 's' },
  session: '',
  sessionTtl: { value: 15, unit: 'm' },
  fallback: false,
}

/** Both Save buttons, in DOM order: [0] = SOCKS, [1] = FlareSolverr. */
function saveButtons(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('button[type="submit"]')
}

describe('SuwayomiPane', () => {
  it('enabling FlareSolverr + entering a URL flips only the FlareSolverr Save button', async () => {
    const wrapper = mount(SuwayomiPane, {
      props: { config: baseConfig, flareSolverr: baseFlareSolverr },
    })

    // Starts clean: nothing edited yet, both Saves disabled.
    let buttons = saveButtons(wrapper)
    expect(buttons[0]!.attributes('disabled')).toBeDefined()
    expect(buttons[1]!.attributes('disabled')).toBeDefined()

    // Flip the FlareSolverr toggle on — a whole-object v-model update.
    const flareToggle = wrapper.find('[aria-label="Enable FlareSolverr"]')
    await flareToggle.trigger('click')

    // The URL field only renders once `modelValue.enabled` is true — its very
    // presence proves the toggle's `update:model-value` actually reached the
    // local `flare` copy (under the const-reactive bug it never does).
    const urlField = wrapper.find('.flare-body .field__input')
    expect(urlField.exists()).toBe(true)
    await urlField.setValue('http://flaresolverr:8191')

    // Only the FlareSolverr Save button reacts — SOCKS stays untouched.
    buttons = saveButtons(wrapper)
    expect(buttons[0]!.attributes('disabled')).toBeDefined()
    expect(buttons[1]!.attributes('disabled')).toBeUndefined()

    await buttons[1]!.trigger('click')

    // §16: the emitted payload carries the full merged FlareSolverr config —
    // a SEPARATE event from `save` (SOCKS), never a nested field.
    const emitted = wrapper.emitted('save-flaresolverr')
    expect(emitted).toBeTruthy()
    const saved = emitted![0]![0] as FlareSolverrConfig
    expect(saved.enabled).toBe(true)
    expect(saved.url).toBe('http://flaresolverr:8191')
    expect(wrapper.emitted('save')).toBeFalsy()
  })

  it('enabling SOCKS flips only the SOCKS Save button and emits `save` with the merged config', async () => {
    const wrapper = mount(SuwayomiPane, {
      props: { config: baseConfig, flareSolverr: baseFlareSolverr },
    })

    let buttons = saveButtons(wrapper)
    expect(buttons[0]!.attributes('disabled')).toBeDefined()

    const socksToggle = wrapper.find('[aria-label="Enable SOCKS proxy"]')
    await socksToggle.trigger('click')

    buttons = saveButtons(wrapper)
    expect(buttons[0]!.attributes('disabled')).toBeUndefined()
    expect(buttons[1]!.attributes('disabled')).toBeDefined()

    await buttons[0]!.trigger('click')
    const emitted = wrapper.emitted('save')
    expect(emitted).toBeTruthy()
    const savedConfig = emitted![0]![0] as SuwayomiConfig
    expect(savedConfig.socks.enabled).toBe(true)
    expect(wrapper.emitted('save-flaresolverr')).toBeFalsy()
  })
})
