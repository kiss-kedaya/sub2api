/**
 * Support-contact entry for a rolled-back deposit.
 *
 * The site already has one public contact field (`contact_info`, shown in the
 * user menu and on the redeem page). A revoked deposit must offer an action, but
 * the field is free text: an admin may store a URL, an email, a Telegram handle
 * or "QQ: 123456789". This helper decides whether it can be turned into a link;
 * anything else is shown as copyable text so the user still gets the value.
 */

const URL_PATTERN = /^https?:\/\//i
const MAILTO_PATTERN = /^mailto:/i
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const TELEGRAM_HANDLE_PATTERN = /^@[A-Za-z][A-Za-z0-9_]{3,}$/
const TELEGRAM_URL_PATTERN = /^(?:t\.me|telegram\.me)\//i

/** A safe href for the configured contact, or '' when it has to stay plain text. */
export function supportContactHref(contact: string | null | undefined): string {
  const value = (contact ?? '').trim()
  if (!value) return ''
  if (URL_PATTERN.test(value) || MAILTO_PATTERN.test(value)) return value
  if (EMAIL_PATTERN.test(value)) return `mailto:${value}`
  if (TELEGRAM_HANDLE_PATTERN.test(value)) return `https://t.me/${value.slice(1)}`
  if (TELEGRAM_URL_PATTERN.test(value)) return `https://${value}`
  // Free text such as "QQ: 123456789" cannot be linked safely.
  return ''
}
