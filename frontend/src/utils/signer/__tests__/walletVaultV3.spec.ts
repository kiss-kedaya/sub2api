import { webcrypto } from 'node:crypto'
import { describe, expect, it } from 'vitest'

import fixture from './fixtures/goInteropVectors.json'
import { detectSignerCrypto, type SignerCrypto } from '../cryptoProvider'
import { hexToBytes } from '../encoding'
import {
  isWalletVaultV3,
  openWalletVaultV3,
  sealWalletVaultV3,
  VAULT_V3_PREFIX,
  vaultCiphertextEquals
} from '../walletVaultV3'

const secureEnv = {
  isSecureContext: true,
  getRandomValues: (array: ArrayBufferView) => webcrypto.getRandomValues(array as Uint8Array)
}

const backends: Array<[string, SignerCrypto]> = [
  ['noble', detectSignerCrypto({ forceBackend: 'noble', env: { ...secureEnv, subtle: null } })],
  ['webcrypto', detectSignerCrypto({ forceBackend: 'webcrypto', env: { ...secureEnv, subtle: webcrypto.subtle as unknown as SubtleCrypto } })]
]

const vector = fixture.vault_vector
const dataKey = hexToBytes(vector.data_key_hex)
const nonce = hexToBytes(vector.nonce_hex)

describe.each(backends)('%s backend matches the Go v3 envelope', (_name, crypto) => {
  it('decrypts the Go-produced ciphertext', async () => {
    expect(isWalletVaultV3(vector.sealed)).toBe(true)
    const contents = await openWalletVaultV3(crypto, dataKey, vector.sealed)
    expect(contents.deposit).toBe('abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about')
    expect(contents.hot).toBe('zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo wrong')
    expect(JSON.stringify(contents)).toBe(vector.plaintext)
  })

  it('seals the exact same bytes for the same nonce', async () => {
    const sealed = await sealWalletVaultV3(crypto, dataKey, vector.plaintext, nonce)
    expect(sealed).toBe(vector.sealed)
    expect(vaultCiphertextEquals(sealed, vector.sealed)).toBe(true)
  })

  it('round-trips with a random nonce and keeps the prefix', async () => {
    const sealed = await sealWalletVaultV3(crypto, dataKey, { deposit: 'd', hot: 'h' })
    expect(sealed.startsWith(VAULT_V3_PREFIX)).toBe(true)
    const payload = sealed.slice(VAULT_V3_PREFIX.length)
    expect(payload.length).toBeGreaterThan(0)
    const opened = await openWalletVaultV3(crypto, dataKey, sealed)
    expect(opened).toMatchObject({ deposit: 'd', hot: 'h' })
  })

  it('rejects a wrong key, wrong prefix and truncated ciphertext', async () => {
    await expect(openWalletVaultV3(crypto, new Uint8Array(32).fill(9), vector.sealed)).rejects.toBeTruthy()
    await expect(openWalletVaultV3(crypto, dataKey, vector.sealed.replace('v3:', 'v2:'))).rejects.toBeTruthy()
    await expect(openWalletVaultV3(crypto, dataKey, `${VAULT_V3_PREFIX}AAAA`)).rejects.toBeTruthy()
  })

  it('authenticates the prefix as AAD (v2/v3 replay is impossible)', async () => {
    // A ciphertext sealed under a different AAD cannot be opened as v3.
    const otherAad = new TextEncoder().encode('sub2api-wallet:v2:')
    const sealed = await crypto.aesGcmSeal(dataKey, nonce, new TextEncoder().encode('{"deposit":"d","hot":"h"}'), otherAad)
    const payload = new Uint8Array(nonce.length + sealed.length)
    payload.set(nonce, 0)
    payload.set(sealed, nonce.length)
    const forged = VAULT_V3_PREFIX + btoa(String.fromCharCode(...payload))
    await expect(openWalletVaultV3(crypto, dataKey, forged)).rejects.toBeTruthy()
  })
})
