import type { Column } from '@/components/common/types'

type Translate = (key: string) => string

/**
 * Columns of the request error log, shared by the ops error table (which renders them) and the
 * usage page's column picker (which lists the same keys). Order follows the admin usage detail:
 * identity (user → key → account) → request shape (platform → model → endpoint → group → type)
 * → outcome (status → message) → time → UA → IP → actions.
 */
export function buildErrorLogColumns(t: Translate): Column[] {
  return [
    { key: 'user', label: t('admin.ops.errorLog.user') },
    { key: 'api_key', label: t('admin.ops.errorLog.apiKey') },
    { key: 'account', label: t('admin.ops.errorLog.account') },
    { key: 'platform', label: t('admin.ops.errorLog.platform') },
    { key: 'model', label: t('admin.ops.errorLog.model'), sortable: true },
    { key: 'endpoint', label: t('admin.ops.errorLog.endpoint') },
    { key: 'group', label: t('admin.ops.errorLog.group') },
    { key: 'type', label: t('admin.ops.errorLog.type') },
    { key: 'category', label: t('usage.errors.category') },
    { key: 'status', label: t('admin.ops.errorLog.status'), sortable: true },
    { key: 'message', label: t('admin.ops.errorLog.message') },
    { key: 'request_id', label: t('admin.ops.errorLog.requestId') },
    { key: 'upstream_request_id', label: t('admin.ops.errorLog.upstreamRequestId') },
    { key: 'created_at', label: t('admin.ops.errorLog.time'), sortable: true },
    { key: 'user_agent', label: t('usage.userAgent') },
    { key: 'client_ip', label: t('admin.ops.errorLog.ip') },
    { key: 'actions', label: t('admin.ops.errorLog.action') },
  ]
}
