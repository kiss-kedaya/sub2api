import DOMPurify from 'dompurify'

export function sanitizeSvg(svg: string): string {
  if (!svg) return ''
  return DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true } })
}
// sanitizeHtml is used for admin-authored page content (home_content). It keeps
// formatting but removes executable/embeddable vectors so a stored payload
// cannot run in every visitor's session.
export function sanitizeHtml(html: string): string {
  if (!html) return ''
  return DOMPurify.sanitize(html, {
    FORBID_TAGS: ['script', 'iframe', 'object', 'embed', 'form', 'style', 'link', 'meta', 'base'],
    FORBID_ATTR: ['onerror', 'onload', 'onclick', 'onmouseover', 'onfocus', 'onbegin', 'formaction'],
    ALLOW_DATA_ATTR: false,
  })
}
