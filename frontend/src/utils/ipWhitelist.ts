/**
 * Helpers for the API key IP whitelist textarea.
 *
 * Both dialogs share the same textarea rules: one entry per line (or separated
 * by spaces/commas, including the full-width comma common in Chinese input),
 * and the "fill in my IP" helper must append rather than overwrite.
 */

/** Split a textarea value into trimmed non-empty IP/CIDR entries. */
export function splitIPEntries(value: string): string[] {
  return value
    .split(/[\s,，]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}

/** True when the entry already exists in the textarea content. */
export function hasIPEntry(value: string, entry: string): boolean {
  const target = entry.trim()
  return target !== '' && splitIPEntries(value).includes(target)
}

/**
 * Append one IP/CIDR to existing textarea content.
 *
 * Returns `duplicate: true` and the original value untouched when the entry is
 * already present, so the caller can show "已存在" instead of writing twice.
 */
export function appendIPEntry(existing: string, entry: string): { value: string; duplicate: boolean } {
  const trimmed = entry.trim()
  if (!trimmed) {
    return { value: existing, duplicate: true }
  }
  if (hasIPEntry(existing, trimmed)) {
    return { value: existing, duplicate: true }
  }
  const base = existing.replace(/[\s,，]+$/, '')
  return { value: base ? `${base}\n${trimmed}` : trimmed, duplicate: false }
}
