/**
 * One-time share export: a .txt download and an A4 print sheet.
 *
 * iOS turns `<a download>` into a preview instead of a file; the caller always
 * offers "复制" as the fallback, and `downloadTextFile` never throws.
 */

export interface ShareSheetInput {
  shareText: string
  slotLabel: string
  shareIndex: number
  createdAt: string
  qrDataUrl?: string
  labels: {
    title: string
    warning: string
    shareLabel: string
    createdAtLabel: string
    recoveryTitle: string
    recoverySteps: string[]
    footer: string
  }
}

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

export function shareFileName(slotLabel: string, shotDate = new Date()): string {
  const stamp = [
    shotDate.getFullYear(),
    String(shotDate.getMonth() + 1).padStart(2, '0'),
    String(shotDate.getDate()).padStart(2, '0')
  ].join('')
  return `sub2api-shard-${slotLabel}-${stamp}.txt`
}

export function downloadTextFile(text: string, filename: string): boolean {
  if (typeof document === 'undefined' || typeof URL === 'undefined') return false
  let url = ''
  try {
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
    url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = filename
    anchor.rel = 'noopener'
    anchor.style.display = 'none'
    document.body.appendChild(anchor)
    anchor.click()
    document.body.removeChild(anchor)
    return true
  } catch {
    return false
  } finally {
    if (url) setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
}

/** A printable A4 sheet with the share, its timestamp, recovery notes and warnings. */
export function buildShareSheetHtml(input: ShareSheetInput): string {
  const { labels } = input
  const steps = labels.recoverySteps.map(step => `<li>${escapeHtml(step)}</li>`).join('')
  const qr = input.qrDataUrl
    ? `<section class="qr"><img src="${escapeHtml(input.qrDataUrl)}" alt="shard qr" width="240" height="240"/></section>`
    : ''
  return `<!doctype html>
<html><head><meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>${escapeHtml(labels.title)}</title>
<style>
  @page { size: A4; margin: 16mm; }
  html, body { margin: 0; padding: 0; background: #fff; color: #111; }
  body { font-family: -apple-system, "Segoe UI", Roboto, "Noto Sans SC", sans-serif; font-size: 12pt; line-height: 1.5; }
  h1 { font-size: 18pt; margin: 0 0 4mm; }
  .warning { border: 2px solid #111; padding: 4mm; margin: 0 0 6mm; font-weight: 600; }
  .share { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 13pt; word-break: break-all; border: 1px dashed #555; padding: 4mm; margin: 0 0 6mm; }
  .meta { margin: 0 0 6mm; color: #333; }
  .qr { margin: 0 0 6mm; text-align: center; }
  h2 { font-size: 13pt; margin: 0 0 2mm; }
  ol { padding-left: 6mm; margin: 0 0 6mm; }
  footer { margin-top: 8mm; font-size: 10pt; color: #444; border-top: 1px solid #999; padding-top: 3mm; }
  @media print { body { -webkit-print-color-adjust: exact; print-color-adjust: exact; } }
</style></head>
<body>
  <h1>${escapeHtml(labels.title)}</h1>
  <div class="warning">${escapeHtml(labels.warning)}</div>
  <div class="share">${escapeHtml(input.shareText)}</div>
  <div class="meta">${escapeHtml(labels.shareLabel)}: ${escapeHtml(input.slotLabel)} · ${escapeHtml(labels.createdAtLabel)}: ${escapeHtml(input.createdAt)}</div>
  ${qr}
  <h2>${escapeHtml(labels.recoveryTitle)}</h2>
  <ol>${steps}</ol>
  <footer>${escapeHtml(labels.footer)}</footer>
</body></html>`
}

/** Open a print window; returns false when the popup was blocked. */
export function openSharePrintWindow(html: string): boolean {
  if (typeof window === 'undefined') return false
  let win: Window | null = null
  try {
    // `noopener` makes window.open return null in current browsers, so we open
    // the same-origin sheet normally and sever the opener link ourselves.
    win = window.open('', '_blank', 'width=900,height=1200')
  } catch {
    return false
  }
  if (!win) return false
  try {
    win.opener = null
    win.document.open()
    win.document.write(html)
    win.document.close()
    win.focus()
    setTimeout(() => {
      try {
        win?.print()
      } catch {
        /* The sheet is still open for manual printing. */
      }
    }, 150)
    return true
  } catch {
    try {
      win.close()
    } catch {
      /* ignore */
    }
    return false
  }
}
