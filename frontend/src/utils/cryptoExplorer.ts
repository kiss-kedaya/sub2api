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

// Address pages live on the same explorers as the transaction pages above.
const explorerAddressBases: Readonly<Record<string, string>> = {
  ethereum: 'https://etherscan.io/address/',
  bsc: 'https://bscscan.com/address/',
  polygon: 'https://polygonscan.com/address/',
  plasma: 'https://plasmascan.to/address/',
  arbitrum: 'https://arbiscan.io/address/',
  base: 'https://basescan.org/address/',
  xlayer: 'https://web3.okx.com/explorer/xlayer/address/',
  solana: 'https://solscan.io/account/'
}

/** Link only recognized addresses, never backend-supplied URLs. */
export function explorerAddressURL(chain: string, address: string): string {
  if (!Object.prototype.hasOwnProperty.call(explorerAddressBases, chain)) return ''
  const base = explorerAddressBases[chain]
  if (!base) return ''
  const valid = chain === 'solana'
    ? /^[1-9A-HJ-NP-Za-km-z]{32,44}$/.test(address)
    : /^0x[0-9a-fA-F]{40}$/.test(address)
  return valid ? base + address : ''
}
