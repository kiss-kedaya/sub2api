// Keep deposit history and the operator ledger on the same explorer mapping.
const explorerBases: Readonly<Record<string, string>> = {
  ethereum: 'https://etherscan.io/tx/',
  bsc: 'https://bscscan.com/tx/',
  polygon: 'https://polygonscan.com/tx/',
  plasma: 'https://plasmascan.to/tx/',
  arbitrum: 'https://arbiscan.io/tx/',
  base: 'https://basescan.org/tx/',
  xlayer: 'https://web3.okx.com/explorer/xlayer/tx/',
  solana: 'https://solscan.io/tx/'
}

/** Link only recognized transaction hashes, never backend-supplied URLs. */
export function explorerTxURL(chain: string, txHash: string): string {
  if (!Object.prototype.hasOwnProperty.call(explorerBases, chain)) return ''
  const base = explorerBases[chain]
  if (!base) return ''
  const valid = chain === 'solana'
    ? /^[1-9A-HJ-NP-Za-km-z]{64,88}$/.test(txHash)
    : /^0x[0-9a-fA-F]{64}$/.test(txHash)
  return valid ? base + txHash : ''
}
