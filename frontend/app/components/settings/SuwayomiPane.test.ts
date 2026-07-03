/**
 * SuwayomiPane — toggle + dirty/Save wiring (F5 regression).
 *
 * Pins the bug: `socks`/`flare` were `reactive(...)` bound with whole-object
 * `v-model`, which desugars to `socks = $event` — reassigning a `const` throws
 * `TypeError: Assignment to constant variable`. Vue swallows the throw, so the
 * local copy never updates, `dirty` never flips, and the Save button stays
 * disabled forever. This test starts with FlareSolverr DISABLED (the shipped
 * fixture already had it enabled, which is why no test caught this), flips the
 * toggle on, types a URL, and asserts the pane actually reacts.
 *
 * Non-vacuous: against the pre-fix `const` binding this throws inside the
 * component and the Save button's `disabled` attribute stays present; after the
 * fix the toggle mutates the local copy in place, `dirty` flips true, and the
 * button's `disabled` attribute is removed.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SuwayomiPane from './SuwayomiPane.vue'
import type { SuwayomiConfig } from '../screens/settings.types'

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
  flareSolverr: {
    enabled: false,
    url: '',
    timeout: { value: 60, unit: 's' },
    session: '',
    sessionTtl: { value: 15, unit: 'm' },
    fallback: false,
  },
}

describe('SuwayomiPane', () => {
  it('enabling FlareSolverr + entering a URL flips dirty and enables Save', async () => {
    const wrapper = mount(SuwayomiPane, {
      props: { config: baseConfig },
    })

    const saveButton = () => wrapper.find('button[type="submit"]')

    // Starts clean: nothing edited yet, Save disabled.
    expect(saveButton().attributes('disabled')).toBeDefined()

    // Flip the FlareSolverr toggle on — a whole-object v-model update.
    const flareToggle = wrapper.find('[aria-label="Enable FlareSolverr"]')
    await flareToggle.trigger('click')

    // The URL field only renders once `modelValue.enabled` is true — its very
    // presence proves the toggle's `update:model-value` actually reached the
    // local `flare` copy (under the const-reactive bug it never does).
    const urlField = wrapper.find('.flare-body .field__input')
    expect(urlField.exists()).toBe(true)
    await urlField.setValue('http://flaresolverr:8191')

    // The local copy actually mutated — Save is now enabled (dirty flipped true).
    expect(saveButton().attributes('disabled')).toBeUndefined()

    await saveButton().trigger('click')

    // §16: the emitted payload carries the full merged config, not a dropped field.
    const emitted = wrapper.emitted('save')
    expect(emitted).toBeTruthy()
    const savedConfig = emitted![0]![0] as SuwayomiConfig
    expect(savedConfig.flareSolverr.enabled).toBe(true)
    expect(savedConfig.flareSolverr.url).toBe('http://flaresolverr:8191')
  })

  it('enabling SOCKS also flips dirty and enables Save', async () => {
    const wrapper = mount(SuwayomiPane, {
      props: { config: baseConfig },
    })

    const saveButton = () => wrapper.find('button[type="submit"]')
    expect(saveButton().attributes('disabled')).toBeDefined()

    const socksToggle = wrapper.find('[aria-label="Enable SOCKS proxy"]')
    await socksToggle.trigger('click')

    expect(saveButton().attributes('disabled')).toBeUndefined()

    await saveButton().trigger('click')
    const emitted = wrapper.emitted('save')
    expect(emitted).toBeTruthy()
    const savedConfig = emitted![0]![0] as SuwayomiConfig
    expect(savedConfig.socks.enabled).toBe(true)
  })
})
