import { ref } from 'vue'

export interface CryptoGrant {
  credential: string
  expires_at: string
  expires_in: number
}

// Payment authority stays in this page's JavaScript memory. It is never written
// to browser storage, URLs, shared HTTP defaults, or an authentication cookie.
let credential = ''
let ownerToken = ''
let expiryTimer: ReturnType<typeof setTimeout> | undefined
export const cryptoGrantExpiresAt = ref(0)

export function currentCryptoSession(): string {
  return localStorage.getItem('auth_token') ?? ''
}

export function clearCryptoGrant() {
  if (expiryTimer) clearTimeout(expiryTimer)
  expiryTimer = undefined
  credential = ''
  ownerToken = ''
  cryptoGrantExpiresAt.value = 0
}

export function getCryptoGrant(): string | undefined {
  if (!credential || !ownerToken || currentCryptoSession() !== ownerToken || Date.now() >= cryptoGrantExpiresAt.value) {
    clearCryptoGrant()
    return undefined
  }
  return credential
}

export function retainCryptoGrant(grant: CryptoGrant, session: string, startedAt: number): string {
  // Cap the client lifetime at the original request start, without sliding it
  // when reused. The backend independently enforces its signed expiry.
  const expiresAt = Math.min(Date.parse(grant.expires_at), startedAt + Math.min(grant.expires_in, 300) * 1000)
  if (!session || session !== currentCryptoSession() || typeof grant.credential !== 'string' || grant.credential.length < 32 ||
      !Number.isFinite(expiresAt) || expiresAt <= Date.now() || !Number.isFinite(grant.expires_in) || grant.expires_in <= 0) {
    clearCryptoGrant()
    throw new Error('Payment verification expired; verify again')
  }
  clearCryptoGrant()
  credential = grant.credential
  ownerToken = session
  cryptoGrantExpiresAt.value = expiresAt
  expiryTimer = setTimeout(clearCryptoGrant, Math.max(0, expiresAt - Date.now()))
  return credential
}

if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    if (event.key === 'auth_token' || event.key === null) clearCryptoGrant()
  })
}

// Only this exact pre-handler refusal proves a retry cannot duplicate an
// operation. Network errors, 5xx, and unrelated permission errors never qualify.
export function isCryptoGrantRejected(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const value = error as { status?: number; reason?: string; code?: string | number }
  return value.status === 403 && (value.reason === 'CRYPTO_GRANT_REQUIRED' || value.code === 'CRYPTO_GRANT_REQUIRED')
}
