import { describe, expect, it } from 'vitest'
import { isClientID, newClientID } from '../tickets'

describe('ticket client ids', () => {
  it('keeps a UUID when randomUUID is missing', () => {
    const cryptoObj = globalThis.crypto
    const original = cryptoObj.randomUUID
    Object.defineProperty(cryptoObj, 'randomUUID', { value: undefined, configurable: true })
    try {
      expect(isClientID(newClientID())).toBe(true)
    } finally {
      Object.defineProperty(cryptoObj, 'randomUUID', { value: original, configurable: true })
    }
  })
})
