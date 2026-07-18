/**
 * NetworkEndpointDialog — the add/edit endpoint form.
 *
 * Pins:
 *   1. Editing a SOCKS endpoint opens the password field BLANK (write-only) even
 *      though the name/host are pre-filled.
 *   2. Submitting an edit emits the full NetworkEndpointInput with the endpoint id
 *      and only the typed password (blank stays blank → the composable omits it).
 *   3. Adding emits id=null with the entered fields; switching kind to FlareSolverr
 *      emits kind='flaresolverr'.
 *   4. Validation blocks submit (no emit) + surfaces the reason inline.
 *
 * The real Dialog teleports through reka-ui's portal (absent in happy-dom), so it
 * is stubbed to render its default + actions slots inline — keeping the assertions
 * on the form's OWN behaviour, not on reka.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import NetworkEndpointDialog from './NetworkEndpointDialog.vue'
import type { NetworkEndpoint } from '../screens/settings.types'

const DialogStub = { template: '<div class="dialog-stub"><slot /><slot name="actions" /></div>' }

const SOCKS: NetworkEndpoint = {
  id: 'ep-socks',
  name: 'VPN SOCKS',
  kind: 'socks',
  enabled: true,
  host: '10.0.1.9',
  port: 1080,
  socksVersion: 5,
  username: 'tsundoku',
  url: '',
  fsProxy: '',
  session: '',
  sessionTtl: 0,
  timeout: 0,
}

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(NetworkEndpointDialog, {
    props: { open: true, ...props },
    global: { stubs: { Dialog: DialogStub } },
  })
}

/** Find the <input> inside the labelled field whose label text matches. */
function fieldInput(wrapper: ReturnType<typeof mountDialog>, label: string) {
  const field = wrapper.findAll('label.field').find(l => l.find('.field__label').text() === label)
  if (!field) throw new Error(`field "${label}" not found`)
  return field.find('input')
}

function saveButton(wrapper: ReturnType<typeof mountDialog>) {
  return wrapper.findAll('button').find(b => b.text() === 'Save changes' || b.text() === 'Add endpoint')!
}

describe('NetworkEndpointDialog', () => {
  it('opens the password field BLANK when editing (write-only), name pre-filled', () => {
    const wrapper = mountDialog({ endpoint: SOCKS })
    expect((fieldInput(wrapper, 'Name').element as HTMLInputElement).value).toBe('VPN SOCKS')
    expect((wrapper.find('input[type="password"]').element as HTMLInputElement).value).toBe('')
  })

  it('emits the edit payload with the id and only a typed password', async () => {
    const wrapper = mountDialog({ endpoint: SOCKS })
    // Leave password blank → not typed.
    await saveButton(wrapper).trigger('click')
    let emitted = wrapper.emitted('submit')
    expect(emitted).toBeTruthy()
    expect(emitted![0]![0]).toMatchObject({ id: 'ep-socks', kind: 'socks', name: 'VPN SOCKS', password: '' })

    // Now type a password and re-submit → it rides along.
    await wrapper.find('input[type="password"]').setValue('newsecret')
    await saveButton(wrapper).trigger('click')
    emitted = wrapper.emitted('submit')
    expect(emitted![1]![0]).toMatchObject({ id: 'ep-socks', password: 'newsecret' })
  })

  it('emits a create payload (id=null) with the entered SOCKS fields', async () => {
    const wrapper = mountDialog({ endpoint: null })
    await fieldInput(wrapper, 'Name').setValue('New proxy')
    await fieldInput(wrapper, 'Host').setValue('proxy.local')
    await saveButton(wrapper).trigger('click')

    const payload = wrapper.emitted('submit')![0]![0]
    expect(payload).toMatchObject({ id: null, kind: 'socks', name: 'New proxy', host: 'proxy.local', port: 1080 })
  })

  it('switches to the FlareSolverr field-group and emits kind=flaresolverr', async () => {
    const wrapper = mountDialog({ endpoint: null })
    await wrapper.findAll('button').find(b => b.text() === 'FlareSolverr')!.trigger('click')
    await fieldInput(wrapper, 'Name').setValue('VPN Flare')
    await fieldInput(wrapper, 'Server URL').setValue('http://flare:8191')
    await saveButton(wrapper).trigger('click')

    const payload = wrapper.emitted('submit')![0]![0]
    expect(payload).toMatchObject({ id: null, kind: 'flaresolverr', name: 'VPN Flare', url: 'http://flare:8191' })
  })

  it('blocks submit and surfaces the reason when the SOCKS host is blank', async () => {
    const wrapper = mountDialog({ endpoint: null })
    await fieldInput(wrapper, 'Name').setValue('Nameless host')
    // Host left blank.
    await saveButton(wrapper).trigger('click')
    expect(wrapper.emitted('submit')).toBeFalsy()
    expect(wrapper.text()).toContain('Enter the SOCKS proxy host')
  })
})
