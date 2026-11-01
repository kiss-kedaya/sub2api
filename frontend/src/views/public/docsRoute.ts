import type { LocationQuery, RouteLocationRaw } from 'vue-router'

/** First docs page; same id as DOCS_DEFAULT_PAGE in data/apiDocs (not imported: it is large). */
export const DOCS_HOME_PAGE = 'quick-start'

/** URL of one docs page: /docs/<page>. */
export function docsLocation(page: string): { path: string } {
  return { path: `/docs/${encodeURIComponent(page)}` }
}

/**
 * /docs and the old /docs?page=<id>[#section] links go to /docs/<id>[#section];
 * other query keys are kept.
 */
export function docsRedirect(to: { query: LocationQuery; hash: string }): RouteLocationRaw {
  const query = { ...to.query }
  const page = typeof query.page === 'string' && query.page ? query.page : DOCS_HOME_PAGE
  delete query.page
  return { ...docsLocation(page), query, hash: to.hash }
}
