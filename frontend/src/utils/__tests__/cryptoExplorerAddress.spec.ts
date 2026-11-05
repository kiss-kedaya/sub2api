import { describe, expect, it } from 'vitest'
import { explorerAddressURL, explorerTxURL } from '../cryptoExplorer'

const evm = '0x' + 'ab'.repeat(20)
const solana = '9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin'

describe('deposit address explorer links', () => {
  it.each([
    ['ethereum', 'https://etherscan.io/address/'],
    ['bsc', 'https://bscscan.com/address/'],
    ['polygon', 'https://polygonscan.com/address/'],
    ['plasma', 'https://plasmascan.to/address/'],
    ['arbitrum', 'https://arbiscan.io/address/'],
    ['base', 'https://basescan.org/address/'],
    ['xlayer', 'https://web3.okx.com/explorer/xlayer/address/']
  ])('uses the %s explorer and keeps the whole address', (chain, base) => {
    expect(explorerAddressURL(chain, evm)).toBe(base + evm)
  })

  it('accepts either letter case in an EVM address', () => {
    expect(explorerAddressURL('bsc', '0x' + 'AB'.repeat(20))).toBe('https://bscscan.com/address/0x' + 'AB'.repeat(20))
  })

  it('links a Solana address on Solscan and checks the base58 length', () => {
    expect(explorerAddressURL('solana', solana)).toBe('https://solscan.io/account/' + solana)
    expect(explorerAddressURL('solana', solana.slice(0, 20))).toBe('')
    expect(explorerAddressURL('solana', solana + solana)).toBe('')
    expect(explorerAddressURL('solana', solana.replace('9', '0'))).toBe('')
    expect(explorerAddressURL('solana', evm)).toBe('')
  })

  it('does not turn an unknown chain, malformed address, or injected URL into a link', () => {
    expect(explorerAddressURL('unknown', evm)).toBe('')
    expect(explorerAddressURL('__proto__', evm)).toBe('')
    expect(explorerAddressURL('constructor', evm)).toBe('')
    expect(explorerAddressURL('bsc', '')).toBe('')
    expect(explorerAddressURL('bsc', '0xshort')).toBe('')
    expect(explorerAddressURL('bsc', evm.slice(0, -1))).toBe('')
    expect(explorerAddressURL('bsc', evm + 'a')).toBe('')
    expect(explorerAddressURL('bsc', '0x' + 'zz'.repeat(20))).toBe('')
    expect(explorerAddressURL('bsc', evm + '?redirect=https://example.test')).toBe('')
    expect(explorerAddressURL('bsc', 'javascript:alert(1)')).toBe('')
    expect(explorerAddressURL('bsc', 'https://example.test/' + evm)).toBe('')
  })

  it('leaves the transaction links as they were', () => {
    const hash = '0x' + 'cd'.repeat(32)
    expect(explorerTxURL('bsc', hash)).toBe('https://bscscan.com/tx/' + hash)
    expect(explorerTxURL('bsc', evm)).toBe('')
  })
})
