import { describe, expect, it } from 'vitest'
import { explorerTxURL } from '../cryptoExplorer'

const hash = '0x' + 'ab'.repeat(32)

describe('deposit transaction explorer links', () => {
  it.each([
    ['ethereum', 'https://etherscan.io/tx/'],
    ['bsc', 'https://bscscan.com/tx/'],
    ['polygon', 'https://polygonscan.com/tx/'],
    ['plasma', 'https://plasmascan.to/tx/'],
    ['arbitrum', 'https://arbiscan.io/tx/'],
    ['base', 'https://basescan.org/tx/'],
    ['xlayer', 'https://web3.okx.com/explorer/xlayer/tx/']
  ])('uses the %s explorer and retains the complete hash', (chain, base) => {
    expect(explorerTxURL(chain, hash)).toBe(base + hash)
  })

  it('does not turn an unknown chain, malformed hash, or injected URL into a link', () => {
    expect(explorerTxURL('unknown', hash)).toBe('')
    expect(explorerTxURL('bsc', '0xshort')).toBe('')
    expect(explorerTxURL('bsc', hash + '?redirect=https://example.test')).toBe('')
    expect(explorerTxURL('bsc', 'javascript:alert(1)')).toBe('')
    expect(explorerTxURL('__proto__', hash)).toBe('')
  })
})
