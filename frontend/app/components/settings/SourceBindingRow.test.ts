/**
 * SourceBindingRow — one per-source assignment row.
 *
 * Pins:
 *   1. An unbound source shows both selects on their global-default option and the
 *      Clear button disabled (nothing to clear).
 *   2. Changing the SOCKS select emits the FULL merged binding (the flare
 *      dimension is preserved, §16).
 *   3. Changing the FlareSolverr select to an endpoint emits flareMode='endpoint'
 *      + the endpoint id; picking "None" emits flareMode='none'.
 *   4. A bound source enables Clear, which emits `clear` with the source id.
 *
 * Non-vacuous: drop the merge (emit only the changed dimension) and test 2 fails;
 * mis-map the endpoint sentinel and test 3 fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SourceBindingRow from './SourceBindingRow.vue'
import type { NetworkEndpoint, NetworkSource, SourceBinding } from '../screens/settings.types'

const source: NetworkSource = { id: '222', name: 'Source A', lang: 'en' }

const socksEndpoints: NetworkEndpoint[] = [{
  id: 'ep-socks', name: 'VPN SOCKS', kind: 'socks', enabled: true,
  host: '10.0.1.9', port: 1080, socksVersion: 5, username: '',
  url: '', fsProxy: '', session: '', sessionTtl: 0, timeout: 0,
}]
const flareEndpoints: NetworkEndpoint[] = [{
  id: 'ep-flare', name: 'VPN Flare', kind: 'flaresolverr', enabled: true,
  host: '', port: 0, socksVersion: 5, username: '',
  url: 'http://flare:8191', fsProxy: '', session: '', sessionTtl: 15, timeout: 60,
}]

function mountRow(props: Record<string, unknown> = {}) {
  return mount(SourceBindingRow, {
    props: { source, binding: null, socksEndpoints, flareEndpoints, ...props },
  })
}

const socksSelect = (w: ReturnType<typeof mountRow>) => w.find('select[aria-label="SOCKS route for Source A"]')
const flareSelect = (w: ReturnType<typeof mountRow>) => w.find('select[aria-label="FlareSolverr route for Source A"]')
const clearButton = (w: ReturnType<typeof mountRow>) => w.findAll('button').find(b => b.text() === 'Use global default')!

describe('SourceBindingRow', () => {
  it('an unbound source sits on the global default with Clear disabled', () => {
    const w = mountRow()
    expect((socksSelect(w).element as HTMLSelectElement).value).toBe('')
    expect((flareSelect(w).element as HTMLSelectElement).value).toBe('global')
    expect(clearButton(w).attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('Global default')
  })

  it('changing the SOCKS select emits the full merged binding', async () => {
    // Start from a bound-to-flare source so the merge must preserve the flare side.
    const binding: SourceBinding = { sourceId: '222', socksEndpointId: null, flareMode: 'endpoint', flareEndpointId: 'ep-flare' }
    const w = mountRow({ binding })
    await socksSelect(w).setValue('ep-socks')

    const payload = w.emitted('set')![0]![0]
    expect(payload).toEqual({
      sourceId: '222',
      socksEndpointId: 'ep-socks',
      flareMode: 'endpoint',
      flareEndpointId: 'ep-flare',
    })
  })

  it('picking a FlareSolverr endpoint emits flareMode=endpoint + its id', async () => {
    const w = mountRow()
    await flareSelect(w).setValue('ep-flare')
    const payload = w.emitted('set')![0]![0]
    expect(payload).toEqual({ sourceId: '222', socksEndpointId: null, flareMode: 'endpoint', flareEndpointId: 'ep-flare' })
  })

  it('picking "None" emits flareMode=none', async () => {
    const w = mountRow()
    await flareSelect(w).setValue('none')
    const payload = w.emitted('set')![0]![0]
    expect(payload).toEqual({ sourceId: '222', socksEndpointId: null, flareMode: 'none', flareEndpointId: null })
  })

  it('a bound source enables Clear, which emits clear with the source id', async () => {
    const binding: SourceBinding = { sourceId: '222', socksEndpointId: 'ep-socks', flareMode: 'global', flareEndpointId: null }
    const w = mountRow({ binding })
    const clear = clearButton(w)
    expect(clear.attributes('disabled')).toBeUndefined()
    await clear.trigger('click')
    expect(w.emitted('clear')![0]).toEqual(['222'])
  })
})
