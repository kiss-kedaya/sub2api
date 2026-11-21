import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

import QrCameraScanner from '../QrCameraScanner.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

async function render(show = true) {
  const wrapper = mount(QrCameraScanner, {
    props: { show },
    global: { stubs: { teleport: true } }
  })
  await flushPromises()
  await new Promise(resolve => setTimeout(resolve, 0))
  return wrapper
}

function setCamera(mediaDevices: unknown): void {
  Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: mediaDevices })
}

function setSecureContext(value: boolean): void {
  Object.defineProperty(window, 'isSecureContext', { configurable: true, value })
}

describe('QR camera scanner fallbacks', () => {
  afterEach(() => {
    delete (navigator as { mediaDevices?: unknown }).mediaDevices
    delete (window as { isSecureContext?: unknown }).isSecureContext
    vi.restoreAllMocks()
  })

  it('uses a playsinline, muted, autoplay video element for iOS Safari', async () => {
    setSecureContext(true)
    setCamera({ getUserMedia: vi.fn().mockRejectedValue(Object.assign(new Error('denied'), { name: 'NotAllowedError' })) })
    const wrapper = await render()
    const video = wrapper.get('video')
    expect(video.attributes()).toHaveProperty('playsinline')
    expect(video.attributes()).toHaveProperty('autoplay')
    expect((video.element as HTMLVideoElement).muted).toBe(true)
  })

  it('explains the http restriction instead of failing without a message', async () => {
    setSecureContext(false)
    const getUserMedia = vi.fn()
    setCamera({ getUserMedia })
    const wrapper = await render()
    expect(wrapper.text()).toContain('admin.crypto.signer.errors.cameraInsecure')
    expect(getUserMedia).not.toHaveBeenCalled()
  })

  it('degrades to a manual fallback message when permission is denied', async () => {
    setSecureContext(true)
    setCamera({ getUserMedia: vi.fn().mockRejectedValue(Object.assign(new Error('denied'), { name: 'NotAllowedError' })) })
    const wrapper = await render()
    expect(wrapper.text()).toContain('admin.crypto.signer.errors.cameraDenied')
  })

  it('reports an unavailable camera and keeps the dialog usable', async () => {
    setSecureContext(true)
    setCamera({ getUserMedia: vi.fn().mockRejectedValue(Object.assign(new Error('no device'), { name: 'NotFoundError' })) })
    const wrapper = await render()
    expect(wrapper.text()).toContain('admin.crypto.signer.errors.cameraUnavailable')
    await wrapper.get('[data-testid="qr-camera-cancel"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})
