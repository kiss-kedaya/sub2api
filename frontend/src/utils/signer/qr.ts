/**
 * Local QR generation/decoding for one-time shares.
 *
 * Everything is bundled (`qrcode` encoder, `jsqr` decoder); no CDN request is
 * ever made, so the shard never leaves the browser. Camera scanning needs a
 * secure context and permission; every failure raises a typed error the UI
 * turns into a manual-paste / upload-image fallback.
 */

import jsQR from 'jsqr'
import QRCode from 'qrcode'

export type QrCameraFailure = 'insecure' | 'unsupported' | 'denied' | 'unavailable'

export class QrCameraError extends Error {
  readonly code: QrCameraFailure

  constructor(code: QrCameraFailure) {
    super(`qr camera unavailable: ${code}`)
    this.name = 'QrCameraError'
    this.code = code
  }
}

export interface QrRenderOptions {
  size?: number
  margin?: number
}

const DEFAULT_QR_SIZE = 256
const DEFAULT_QR_MARGIN = 2

export async function shareQrDataUrl(text: string, options: QrRenderOptions = {}): Promise<string> {
  if (!text) throw new Error('qr payload is empty')
  return QRCode.toDataURL(text, {
    errorCorrectionLevel: 'M',
    margin: options.margin ?? DEFAULT_QR_MARGIN,
    width: options.size ?? DEFAULT_QR_SIZE,
    color: { dark: '#000000', light: '#ffffff' }
  })
}

export interface CameraSupport {
  supported: boolean
  reason?: QrCameraFailure
}

export function detectQrCameraSupport(): CameraSupport {
  if (typeof navigator === 'undefined') return { supported: false, reason: 'unsupported' }
  const globalSecure = typeof window !== 'undefined' ? window.isSecureContext : undefined
  if (globalSecure === false) return { supported: false, reason: 'insecure' }
  const media = navigator.mediaDevices as MediaDevices | undefined
  if (!media || typeof media.getUserMedia !== 'function') return { supported: false, reason: 'unsupported' }
  return { supported: true }
}

function cameraErrorCode(error: unknown): QrCameraFailure {
  const name = error && typeof error === 'object' ? String((error as { name?: unknown }).name ?? '') : ''
  if (name === 'NotAllowedError' || name === 'SecurityError' || name === 'PermissionDeniedError') return 'denied'
  return 'unavailable'
}

/**
 * Open the rear camera where possible. iOS needs `playsinline` on the video
 * element (the component sets it) and `facingMode` may be unsupported, so we
 * retry with a generic video constraint before reporting failure.
 */
export async function openQrCamera(): Promise<MediaStream> {
  const support = detectQrCameraSupport()
  if (!support.supported) throw new QrCameraError(support.reason ?? 'unsupported')
  const media = navigator.mediaDevices
  try {
    return await media.getUserMedia({
      audio: false,
      video: {
        facingMode: { ideal: 'environment' },
        width: { ideal: 1280 },
        height: { ideal: 720 }
      }
    })
  } catch (firstError) {
    const firstCode = cameraErrorCode(firstError)
    if (firstCode !== 'unavailable') throw new QrCameraError(firstCode)
    try {
      return await media.getUserMedia({ audio: false, video: true })
    } catch (secondError) {
      throw new QrCameraError(cameraErrorCode(secondError))
    }
  }
}

export function stopMediaStream(stream: MediaStream | null | undefined): void {
  if (!stream) return
  for (const track of stream.getTracks()) {
    try {
      track.stop()
    } catch {
      /* ignore */
    }
  }
}

export function decodeQrPixels(data: Uint8ClampedArray, width: number, height: number): string | null {
  if (!width || !height || data.length < width * height * 4) return null
  try {
    return jsQR(data, width, height, { inversionAttempts: 'attemptBoth' })?.data ?? null
  } catch {
    return null
  }
}

function loadImage(file: Blob): Promise<HTMLImageElement | ImageBitmap> {
  if (typeof createImageBitmap === 'function') {
    return createImageBitmap(file).catch(() => loadViaImageElement(file))
  }
  return loadViaImageElement(file)
}

function loadViaImageElement(file: Blob): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    if (typeof Image === 'undefined' || typeof URL === 'undefined') {
      reject(new Error('image decoding is unavailable'))
      return
    }
    const url = URL.createObjectURL(file)
    const image = new Image()
    image.onload = () => {
      URL.revokeObjectURL(url)
      resolve(image)
    }
    image.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new Error('image could not be decoded'))
    }
    image.src = url
  })
}

/** Decode a QR code from an uploaded image file (no camera needed). */
export async function decodeQrFromImageFile(file: Blob): Promise<string | null> {
  const image = await loadImage(file)
  const width = 'naturalWidth' in image ? image.naturalWidth : image.width
  const height = 'naturalHeight' in image ? image.naturalHeight : image.height
  if (!width || !height) throw new Error('image has no pixels')
  if (typeof document === 'undefined') throw new Error('canvas is unavailable')
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const context = canvas.getContext('2d')
  if (!context) throw new Error('canvas 2d context is unavailable')
  context.drawImage(image as CanvasImageSource, 0, 0, width, height)
  const pixels = context.getImageData(0, 0, width, height)
  return decodeQrPixels(pixels.data, width, height)
}

/** Grab one frame from the live preview and try to decode it. */
export function decodeQrFromVideoFrame(video: HTMLVideoElement, canvas: HTMLCanvasElement): string | null {
  const width = video.videoWidth
  const height = video.videoHeight
  if (!width || !height) return null
  canvas.width = width
  canvas.height = height
  const context = canvas.getContext('2d', { willReadFrequently: true })
  if (!context) return null
  context.drawImage(video, 0, 0, width, height)
  const pixels = context.getImageData(0, 0, width, height)
  return decodeQrPixels(pixels.data, width, height)
}
