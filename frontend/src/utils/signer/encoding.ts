/** Byte helpers shared by the signer modules. No Node/Buffer dependency. */

const BASE64_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'

export function concatBytes(...parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((sum, part) => sum + part.length, 0)
  const out = new Uint8Array(total)
  let offset = 0
  for (const part of parts) {
    out.set(part, offset)
    offset += part.length
  }
  return out
}

export function equalBytes(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false
  let different = 0
  for (let i = 0; i < a.length; i += 1) different |= a[i] ^ b[i]
  return different === 0
}

export function bytesToBase64(bytes: Uint8Array): string {
  let out = ''
  for (let i = 0; i < bytes.length; i += 3) {
    const b0 = bytes[i]
    const b1 = i + 1 < bytes.length ? bytes[i + 1] : 0
    const b2 = i + 2 < bytes.length ? bytes[i + 2] : 0
    out += BASE64_ALPHABET[b0 >> 2]
    out += BASE64_ALPHABET[((b0 & 0x03) << 4) | (b1 >> 4)]
    out += i + 1 < bytes.length ? BASE64_ALPHABET[((b1 & 0x0f) << 2) | (b2 >> 6)] : '='
    out += i + 2 < bytes.length ? BASE64_ALPHABET[b2 & 0x3f] : '='
  }
  return out
}

export function base64ToBytes(text: string): Uint8Array {
  const normalized = text.replace(/\s+/g, '')
  if (!normalized) return new Uint8Array(0)
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(normalized)) throw new Error('invalid base64')
  const unpadded = normalized.replace(/=+$/, '')
  if (unpadded.length % 4 === 1) throw new Error('invalid base64')
  const out = new Uint8Array(Math.floor((unpadded.length * 3) / 4))
  let buffer = 0
  let bits = 0
  let offset = 0
  for (const char of unpadded) {
    const index = BASE64_ALPHABET.indexOf(char)
    if (index < 0) throw new Error('invalid base64')
    buffer = (buffer << 6) | index
    bits += 6
    if (bits >= 8) {
      bits -= 8
      out[offset] = (buffer >> bits) & 0xff
      offset += 1
    }
  }
  return out
}

export function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
}

export function hexToBytes(hex: string): Uint8Array {
  if (hex.length % 2 !== 0 || !/^[0-9a-fA-F]*$/.test(hex)) throw new Error('invalid hex')
  const out = new Uint8Array(hex.length / 2)
  for (let i = 0; i < out.length; i += 1) out[i] = Number.parseInt(hex.slice(i * 2, i * 2 + 2), 16)
  return out
}

export function utf8Bytes(text: string): Uint8Array {
  return new TextEncoder().encode(text)
}

export function utf8Text(bytes: Uint8Array): string {
  return new TextDecoder().decode(bytes)
}

/** Best-effort overwrite; JS strings cannot be zeroed, arrays can. */
export function zeroBytes(...arrays: Array<Uint8Array | null | undefined>): void {
  for (const array of arrays) if (array) array.fill(0)
}

/** Stable, copy-free view of a byte array as ArrayBuffer for WebCrypto. */
export function toArrayBuffer(bytes: Uint8Array): ArrayBuffer {
  return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer
}
