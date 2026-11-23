/**
 * One-time-code helpers shared by every segmented code field.
 *
 * The numeric keyboards and IMEs Chinese users actually type on submit
 * full-width digits (１２３) and stray separators ("123 456"); every code field
 * must fold both to plain ASCII digits before it validates or submits.
 */
export function normalizeOtpDigits(value: string): string {
  return value
    .replace(/[０-９]/g, digit => String.fromCharCode(digit.charCodeAt(0) - 0xfee0))
    .replace(/[^0-9]/g, '')
}
