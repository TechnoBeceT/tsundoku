// @vitest-environment node

import { describe, expect, expectTypeOf, it } from 'vitest'

import type { components, operations, paths } from './schema'

type Schemas = components['schemas']
type EffectiveConfiguration = components['schemas']['SourceEffectiveConfiguration']
type TransportUpdate = components['schemas']['SourceTransportPolicyUpdate']
type ImageProxyMembershipUpdate = components['schemas']['SourceImageProxyMembershipUpdate']
type EffectiveResponse = operations['getSourceEffectiveConfiguration']['responses'][200]['content']['application/json']
type ExceptionsResponse = operations['listSourceExceptions']['responses'][200]['content']['application/json']
type TransportRequest = operations['updateSourceTransport']['requestBody']['content']['application/json']
type ImageProxyMembershipRequest = operations['updateSourceImageProxy']['requestBody']['content']['application/json']
type MutationResponse = operations['updateSourceTransport']['responses'][200]['content']['application/json']
type BindingPut = NonNullable<paths['/api/network/bindings/{sourceId}']['put']>
type BindingDelete = NonNullable<paths['/api/network/bindings/{sourceId}']['delete']>

const bindingPutNotFound: BindingPut['responses'][404]['content']['application/json'] = {
  message: 'source not found',
}

const runtime: Schemas['SourceRuntimeStatus'] = {
  status: 'pending',
  desiredRevision: 9,
  appliedRevision: 8,
  lastApplyAttempt: null,
  lastApplyError: 'engine unavailable',
}

const configuration: EffectiveConfiguration = {
  source: {
    sourceId: '1998416842837112832',
    name: 'Comix',
    language: 'en',
  },
  downloadConcurrency: { override: null, effective: 4, inherited: true },
  imageRequestDelay: { override: '750ms', effective: '750ms', inherited: false },
  protection: {
    warmupInterval: '15m',
    warmupSlowThresholdMs: 2500,
    failureThreshold: 3,
    sourceCooldown: '10m',
    politenessDelay: '1s',
  },
  bypassEnabled: true,
  reuseBypassSession: {
    override: null,
    effective: true,
    inherited: true,
    mode: 'reusable',
  },
  imageConnectionMode: {
    override: 'fresh',
    effective: 'fresh',
    inherited: false,
  },
  imageProxy: {
    optedIn: true,
    gatewayEnabled: true,
    gatewayConfigured: true,
    effectiveAvailable: true,
  },
  routing: {
    socksMode: 'endpoint',
    socks: { endpointId: 'socks-id', name: 'VPN' },
    bypassMode: 'global',
    bypass: { endpointId: null, name: null },
  },
  profileKey: 'source-1998416842837112832',
  runtime,
}

describe('source configuration generated contract', () => {
  it('exposes the exact generated schemas used by source settings', () => {
    expectTypeOf<EffectiveResponse>().toEqualTypeOf<EffectiveConfiguration>()
    expectTypeOf<TransportRequest>().toEqualTypeOf<TransportUpdate>()
    expectTypeOf<ImageProxyMembershipRequest>().toEqualTypeOf<ImageProxyMembershipUpdate>()
  })

  it('keeps signed int64 source ids as exact path strings', () => {
    const transportSourceId: operations['updateSourceTransport']['parameters']['path']['sourceId'] = '-42'
    const bindingSourceId: BindingPut['parameters']['path']['sourceId'] = '1998416842837112832'
    const clearSourceId: BindingDelete['parameters']['path']['sourceId'] = '-42'

    expect([transportSourceId, bindingSourceId, clearSourceId]).toEqual([
      '-42',
      '1998416842837112832',
      '-42',
    ])
    expect(configuration.source.sourceId).toBe('1998416842837112832')
  })

  it('preserves nullable inherited values and transport patch enums', () => {
    const update: TransportRequest = {
      reuseBypassSession: { mode: 'inherit' },
      imageConnectionMode: { mode: 'override', value: 'reuse' },
    }

    expect(configuration.downloadConcurrency.override).toBeNull()
    expect(configuration.reuseBypassSession.mode).toBe('reusable')
    expect(configuration.imageConnectionMode.override).toBe('fresh')
    expect(update).toEqual({
      reuseBypassSession: { mode: 'inherit' },
      imageConnectionMode: { mode: 'override', value: 'reuse' },
    })
  })

  it('keeps image proxy membership explicit and reports effective availability separately', () => {
    const update: ImageProxyMembershipUpdate = { enabled: false }

    expect(update.enabled).toBe(false)
    expect(configuration.imageProxy).toEqual({
      optedIn: true,
      gatewayEnabled: true,
      gatewayConfigured: true,
      effectiveAvailable: true,
    })
  })

  it('resolves SOCKS and bypass routing without collapsing their modes', () => {
    expect(configuration.routing).toEqual({
      socksMode: 'endpoint',
      socks: { endpointId: 'socks-id', name: 'VPN' },
      bypassMode: 'global',
      bypass: { endpointId: null, name: null },
    })
  })

  it('returns the effective configuration and runtime from every mutation', () => {
    const transport: MutationResponse = { configuration, runtime }
    const bindingPut: BindingPut['responses'][200]['content']['application/json'] = transport
    const bindingDelete: BindingDelete['responses'][200]['content']['application/json'] = transport

    expect(transport.configuration.profileKey).toBe('source-1998416842837112832')
    expect(bindingPut.runtime).toEqual({
      status: 'pending',
      desiredRevision: 9,
      appliedRevision: 8,
      lastApplyAttempt: null,
      lastApplyError: 'engine unavailable',
    })
    expect(bindingDelete.runtime.appliedRevision).toBe(8)
    expect(bindingPutNotFound.message).toBe('source not found')
  })

  it('lists field-level exceptions with runtime status', () => {
    const summaries: ExceptionsResponse = [{
      source: configuration.source,
      exceptionCount: 5,
      runtime,
    }]

    expect(summaries[0]?.exceptionCount).toBe(5)
    expect(summaries[0]?.runtime.lastApplyAttempt).toBeNull()
  })
})
