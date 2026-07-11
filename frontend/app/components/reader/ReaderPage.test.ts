/**
 * ReaderPage — component tests for the preload behaviour: pages near the centred
 * position load eagerly at high priority, while distant pages stay lazy.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ReaderPage from './ReaderPage.vue'

describe('ReaderPage preload', () => {
  const testUrl = 'https://example.com/page.png'

  it('renders loading="eager" + fetchpriority="high" when distanceFromCentre <= PRELOAD_RADIUS', () => {
    const wrapper = mount(ReaderPage, {
      props: {
        src: testUrl,
        alt: 'Page 1',
        distanceFromCentre: 2, // Within radius of 3
      },
    })

    const img = wrapper.find('img')
    expect(img.exists()).toBe(true)
    expect(img.attributes('loading')).toBe('eager')
    expect(img.attributes('fetchpriority')).toBe('high')
  })

  it('renders loading="lazy" + fetchpriority="auto" when distanceFromCentre > PRELOAD_RADIUS', () => {
    const wrapper = mount(ReaderPage, {
      props: {
        src: testUrl,
        alt: 'Page 1',
        distanceFromCentre: 5, // Beyond radius of 3
      },
    })

    const img = wrapper.find('img')
    expect(img.exists()).toBe(true)
    expect(img.attributes('loading')).toBe('lazy')
    expect(img.attributes('fetchpriority')).toBe('auto')
  })

  it('renders loading="eager" + fetchpriority="high" when distanceFromCentre === PRELOAD_RADIUS', () => {
    const wrapper = mount(ReaderPage, {
      props: {
        src: testUrl,
        alt: 'Page 1',
        distanceFromCentre: 3, // Exactly at radius boundary
      },
    })

    const img = wrapper.find('img')
    expect(img.exists()).toBe(true)
    expect(img.attributes('loading')).toBe('eager')
    expect(img.attributes('fetchpriority')).toBe('high')
  })

  it('renders loading="lazy" + fetchpriority="auto" when distanceFromCentre is not provided (defaults to Infinity)', () => {
    const wrapper = mount(ReaderPage, {
      props: {
        src: testUrl,
        alt: 'Page 1',
        // distanceFromCentre defaults to Infinity
      },
    })

    const img = wrapper.find('img')
    expect(img.exists()).toBe(true)
    expect(img.attributes('loading')).toBe('lazy')
    expect(img.attributes('fetchpriority')).toBe('auto')
  })

  it('transitions from eager to lazy when distanceFromCentre changes', async () => {
    const wrapper = mount(ReaderPage, {
      props: {
        src: testUrl,
        alt: 'Page 1',
        distanceFromCentre: 2, // Within radius
      },
    })

    let img = wrapper.find('img')
    expect(img.attributes('loading')).toBe('eager')
    expect(img.attributes('fetchpriority')).toBe('high')

    // Change distance beyond radius
    await wrapper.setProps({ distanceFromCentre: 5 })

    img = wrapper.find('img')
    expect(img.attributes('loading')).toBe('lazy')
    expect(img.attributes('fetchpriority')).toBe('auto')
  })

  it('does not render img when src is empty', () => {
    const wrapper = mount(ReaderPage, {
      props: {
        src: '',
        alt: 'Page 1',
        distanceFromCentre: 0,
      },
    })

    const img = wrapper.find('img')
    expect(img.exists()).toBe(false)
  })

  it('does not render img when load fails', async () => {
    const wrapper = mount(ReaderPage, {
      props: {
        src: 'https://example.invalid/missing.png',
        alt: 'Page 1',
        distanceFromCentre: 0,
      },
    })

    const img = wrapper.find('img')
    expect(img.exists()).toBe(true)

    // Simulate load failure
    await img.trigger('error')

    const imgAfterError = wrapper.find('img')
    expect(imgAfterError.exists()).toBe(false)

    // Should show placeholder instead
    const placeholder = wrapper.find('.page__placeholder')
    expect(placeholder.exists()).toBe(true)
  })
})
