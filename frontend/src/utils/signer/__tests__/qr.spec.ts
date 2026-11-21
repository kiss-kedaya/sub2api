import { afterEach, describe, expect, it, vi } from 'vitest'

import { detectQrCameraSupport, openQrCamera, QrCameraError, stopMediaStream } from '../qr'

function installMediaDevices(value: unknown): void {
  Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value })
}

function installSecureContext(value: boolean): void {
  Object.defineProperty(window, 'isSecureContext', { configurable: true, value })
}

describe('QR camera fallbacks (iOS / permission / http)', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    delete (navigator as { mediaDevices?: unknown }).mediaDevices
    delete (window as { isSecureContext?: unknown }).isSecureContext
  })

  it('disables scanning on a non-secure context instead of failing silently', () => {
    installSecureContext(false)
    installMediaDevices({ getUserMedia: vi.fn() })
    expect(detectQrCameraSupport()).toEqual({ supported: false, reason: 'insecure' })
    return expect(openQrCamera()).rejects.toMatchObject({ code: 'insecure' })
  })

  it('reports an unsupported browser when getUserMedia is missing', () => {
    installSecureContext(true)
    installMediaDevices(undefined)
    expect(detectQrCameraSupport()).toEqual({ supported: false, reason: 'unsupported' })
  })

  it('raises a typed denied error when permission is refused, never a hard failure', async () => {
    installSecureContext(true)
    const denied = new Error('denied')
    denied.name = 'NotAllowedError'
    installMediaDevices({ getUserMedia: vi.fn().mockRejectedValue(denied) })
    await expect(openQrCamera()).rejects.toBeInstanceOf(QrCameraError)
    await expect(openQrCamera()).rejects.toMatchObject({ code: 'denied' })
  })

  it('retries with a generic video constraint when facingMode is unsupported', async () => {
    installSecureContext(true)
    const stream = { getTracks: () => [] }
    const getUserMedia = vi.fn()
      .mockRejectedValueOnce(Object.assign(new Error('overconstrained'), { name: 'OverconstrainedError' }))
      .mockResolvedValueOnce(stream)
    installMediaDevices({ getUserMedia })
    await expect(openQrCamera()).resolves.toBe(stream)
    expect(getUserMedia).toHaveBeenCalledTimes(2)
    expect(getUserMedia.mock.calls[1][0]).toEqual({ audio: false, video: true })
  })

  it('stops every track when the scanner closes', () => {
    const stopA = vi.fn()
    const stopB = vi.fn()
    stopMediaStream({ getTracks: () => [{ stop: stopA }, { stop: stopB }] } as unknown as MediaStream)
    expect(stopA).toHaveBeenCalledTimes(1)
    expect(stopB).toHaveBeenCalledTimes(1)
  })
})
