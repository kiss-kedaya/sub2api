import { sanitizeUrl } from '@/utils/url'

export const INTERNAL_DOCS_URL = '/docs?page=quick-start'

/** Keep an operator's valid external docs URL; otherwise open this site's docs. */
export function resolveDocsUrl(value: string | null | undefined): string {
  return sanitizeUrl(value || '', { allowRelative: true }) || INTERNAL_DOCS_URL
}
