import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

import vectors from '@/utils/signer/__tests__/fixtures/goInteropVectors.json'
import { detectSignerCrypto } from '@/utils/signer/cryptoProvider'
import ShareInputPanel from '../ShareInputPanel.vue'

const decodeQrFromImageFile = vi.hoisted(() => vi.fn())
vi.mock('@/utils/signer/qr', async () => {
  const actual = await vi.importActual<typeof import('@/utils/signer/qr')>('@/utils/signer/qr')
  return { ...actual, decodeQrFromImageFile }
})
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

const crypto = detectSignerCrypto({
  forceBackend: 'noble',
  env: {
    isSecureContext: true,
    getRandomValues: (array: ArrayBufferView) => webcrypto.getRandomValues(array as Uint8Array),
    subtle: null
  }
})
const share = vectors.shard_vectors[2].shares[1]

function paste(wrapper: ReturnType<typeof mount>, raw: string): void {
  const textarea = wrapper.get('textarea').element
  const event = new Event('paste', { bubbles: true }) as Event & { clipboardData?: unknown }
  event.clipboardData = { getData: () => raw }
  textarea.dispatchEvent(event)
}

async function render(modelValue = '') {
  const wrapper = mount(ShareInputPanel, {
    props: { modelValue, crypto },
    global: { stubs: { QrCameraScanner: true } }
  })
  await flushPromises()
  return wrapper
}

describe('share input normalization and fallbacks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    decodeQrFromImageFile.mockResolvedValue(null)
  })

  it('normalizes pasted lowercase, spaced and dash-separated input', async () => {
    const wrapper = await render()
    paste(wrapper, ` ${share.toLowerCase().replace(/-/g, ' - ')} `)
    await flushPromises()
    const emitted = wrapper.emitted('update:modelValue') ?? []
    expect(emitted[emitted.length - 1]).toEqual([share.replace(/-/g, '')])
    expect(wrapper.text()).toContain('admin.crypto.signer.shareInput.pasteNormalized')
  })

  it('normalizes full-width input and zero-width characters', async () => {
    const wrapper = await render()
    paste(wrapper, `\u200b${share.replace(/S2V1/, 'Ｓ２Ｖ１')}\uFEFF`)
    await flushPromises()
    const emitted = wrapper.emitted('update:modelValue') ?? []
    expect(emitted[emitted.length - 1]).toEqual([share.replace(/-/g, '')])
  })

  it('reports invalid characters instead of silently dropping them', async () => {
    const wrapper = await render()
    paste(wrapper, 'S2V1-ABCD-＃＃＃')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.crypto.signer.errors.shareInvalidCharacter')
  })

  it('emits a parsed share with index/threshold metadata for valid input', async () => {
    const wrapper = await render()
    await wrapper.get('textarea').setValue(share)
    await flushPromises()
    const parsed = wrapper.emitted('parsed') ?? []
    const last = parsed[parsed.length - 1]?.[0] as { index: number; threshold: number; total: number } | null
    expect(last).toMatchObject({ index: 2, threshold: 2, total: 3 })
  })

  it('falls back to manual paste when the camera is unsupported', async () => {
    Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: undefined })
    const wrapper = await render()
    await wrapper.get('[data-testid="share-input-scan"]').trigger('click')
    expect(wrapper.text()).toContain('admin.crypto.signer.errors.cameraUnsupported')
    delete (navigator as { mediaDevices?: unknown }).mediaDevices
  })

  it('falls back to manual paste when the QR image contains no code', async () => {
    decodeQrFromImageFile.mockResolvedValue(null)
    const wrapper = await render()
    const fileInput = wrapper.get('[data-testid="share-input-file"]')
    const file = new File(['x'], 'qr.png', { type: 'image/png' })
    Object.defineProperty(fileInput.element, 'files', { configurable: true, value: [file] })
    await fileInput.trigger('change')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.crypto.signer.shareInput.uploadFailed')
    expect(decodeQrFromImageFile).toHaveBeenCalled()
  })

  it('applies a share decoded from an uploaded QR image', async () => {
    decodeQrFromImageFile.mockResolvedValue(share)
    const wrapper = await render()
    const fileInput = wrapper.get('[data-testid="share-input-file"]')
    Object.defineProperty(fileInput.element, 'files', {
      configurable: true,
      value: [new File(['x'], 'qr.png', { type: 'image/png' })]
    })
    await fileInput.trigger('change')
    await flushPromises()
    const emitted = wrapper.emitted('update:modelValue') ?? []
    expect(emitted[emitted.length - 1]).toEqual([share.replace(/-/g, '')])
    expect(wrapper.text()).toContain('admin.crypto.signer.shareInput.uploaded')
  })
})
