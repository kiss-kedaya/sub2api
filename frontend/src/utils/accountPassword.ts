/**
 * Best-effort answer to "can this account decrypt something with its login password?"
 *
 * OAuth sign-ups in sub2api get a random password the user never learns, but they
 * may still bind an email and later reset it. The one case we can prove from the
 * profile we already fetch is a synthetic sign-in email: those users were created
 * by a third-party provider without a real mailbox, so they have no password they
 * could type into a local KDF.
 *
 * Keep this in one place: the key-seal opt-in uses it to hide the checkbox and
 * explain why, instead of letting the user encrypt with a password they cannot
 * reproduce. It only reads profile data; it never calls the network.
 */

import type { User } from '@/types'

/** Mirrors the backend `*ConnectSyntheticEmailDomain` constants. */
export const SYNTHETIC_OAUTH_EMAIL_SUFFIXES = [
  '@linuxdo-connect.invalid',
  '@oidc-connect.invalid',
  '@wechat-connect.invalid',
  '@dingtalk-connect.invalid',
] as const

export function hasSyntheticOAuthEmail(email: string | null | undefined): boolean {
  const normalized = (email ?? '').trim().toLowerCase()
  if (!normalized) return false
  return SYNTHETIC_OAUTH_EMAIL_SUFFIXES.some((suffix) => normalized.endsWith(suffix))
}

/**
 * True when the profile shows a third-party sign-up that has no email/password
 * of its own, so the "use my login password to view this key later" option must
 * stay hidden. Unknown profiles default to "has a password" so a missing fetch
 * never hides the feature for normal accounts.
 */
export function accountLacksLoginPassword(user: Pick<User, 'email'> | null | undefined): boolean {
  if (!user) return false
  return hasSyntheticOAuthEmail(user.email)
}