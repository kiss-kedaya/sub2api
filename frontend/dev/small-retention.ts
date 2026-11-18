import type { SmallDetail, SmallRecord, SmallUsage } from '../src/api/admin/smallRequestRetention'

// Synthetic local-only evidence. No network calls or persistent storage.
export function createSmallRetentionDemo(now: Date) {
  const iso = (minutesAgo: number) => new Date(now.getTime() - minutesAgo * 60_000).toISOString()
  const counts = [360, 1, 7, 48, 1, 128, 4, 2, 14, 1, 250, 3]
  const fixtures: SmallDetail[] = counts.map((count, index) => {
    const id = index + 1, status = [200, 200, 429, 200, 400, 200, 502, 503, 200, 401, 200, 500][index]!
    const stream = index % 3 === 0
    const model = ['demo-text-pro', 'demo-text-mini', 'demo-reasoning'][index % 3]!
    const reasons = index === 0 ? ['external_context:previous_response_id'] : index === 6 ? ['client_delivery_incomplete', 'stream_terminal_missing'] : index === 8 ? ['external_context:file_id'] : []
    const requestID = `demo-request-${String(id).padStart(3, '0')}`
    const body = JSON.stringify({ model, input: 'Please summarize this synthetic invoice: 3 items, USD 12.50 total.', stream, ...(index === 0 ? { previous_response_id: 'demo-previous-response' } : {}), ...(index === 8 ? { file_id: 'demo-external-file' } : {}) }, null, 2)
    const usage: SmallUsage = {
      request_id: requestID, user_id: 910101 + index % 4, api_key_id: 930101 + index % 4,
      account_id: 920101 + index % 3, group_id: 940101, platform: 'openai', model, upstream_model: model, stream,
      input_tokens: 64, cache_read_tokens: 128, cache_creation_tokens: 16, output_tokens: status >= 400 ? 0 : 48,
      actual_cost: status >= 400 || index === 1 ? '0.00000000' : '0.00000009', calculated_cost: '0.00000009',
      settlement: status >= 500 ? 'failed' : status >= 400 ? 'not_billed_no_usage_row' : index === 1 ? 'not_billed' : 'settled', duration_ms: 1280 + index * 81,
    }
    const record: SmallRecord = {
      id, bucket: iso(60 * (index + 1)), user_id: usage.user_id, api_key_id: usage.api_key_id, model, status,
      first_seen: iso(60 * (index + 1)), last_seen: iso(index * 7 + 1), expires_at: iso(-60 * (23 - index)), repeat_count: count,
      input_tokens: usage.input_tokens * count, cache_read_tokens: usage.cache_read_tokens * count, cache_creation_tokens: usage.cache_creation_tokens * count, output_tokens: usage.output_tokens * count,
      actual_cost: (Number(usage.actual_cost) * (count - (count >= 10 ? 1 : 0))).toFixed(8), raw_bytes: count * 1600,
      omitted_responses: index === 0 ? 18 : 0, request_ids: [requestID, ...(count > 1 ? [requestID + '-latest'] : [])],
      data: { method: 'POST', path: index % 2 ? '/v1/chat/completions' : '/v1/responses', group_id: usage.group_id, replay_reasons: reasons, first_usage: usage },
    }
    const responseCount = count >= 10 ? 3 : 1
    const retained = count - record.omitted_responses
    const responses: SmallDetail['responses'] = Array.from({ length: responseCount }, (_, sampleIndex) => {
      const repeatCount = sampleIndex === 0 ? retained - (responseCount - 1) : 1
      const text = `Synthetic response sample ${sampleIndex + 1}: invoice total is USD 12.50.`
      const responseBody = stream
        ? `event: response.output_text.delta\ndata: ${JSON.stringify({ type: 'response.output_text.delta', delta: text })}\n\nevent: response.completed\ndata: ${JSON.stringify({ type: 'response.completed', response: { id: 'demo-response-' + id, status: 'completed', usage: { input_tokens: 208, output_tokens: 48 } } })}\n\n`
        : JSON.stringify(status >= 400 ? { error: { message: 'Synthetic upstream error for local reconciliation preview', type: 'demo_error' } } : { id: 'demo-response-' + id, output_text: text, usage: { input_tokens: 208, output_tokens: 48 } }, null, 2)
      const retainedBody = index === 6 ? responseBody.split('event: response.completed')[0]! : responseBody
      return { fingerprint: `demo-response-${id}-${sampleIndex}`, first_seen: sampleIndex === 0 ? record.first_seen : record.last_seen, last_seen: record.last_seen, repeat_count: repeatCount,
        data: { status, usage: { ...usage, ...(sampleIndex === 1 ? { settlement: 'already_settled', actual_cost: '0.00000000' } : {}) }, partial: index === 6, replay_reasons: reasons },
        headers: { 'content-type': [stream ? 'text/event-stream' : 'application/json'], 'x-request-id': [requestID], 'cache-control': ['no-store'] }, body: retainedBody }
    })
    return { record, request_headers: { authorization: ['<REPLACE_CREDENTIAL>'], 'content-type': ['application/json'], 'x-client-request-id': [requestID], 'x-demo-note': ['Synthetic evidence only', 'No real credentials'] }, request_body: body,
      curl: `curl --request 'POST' 'https://example.invalid${record.data.path}' \\\n  --header 'authorization: <REPLACE_CREDENTIAL>' \\\n  --header 'content-type: application/json' \\\n  --data-binary '${body}'`, responses }
  })
  let enabled = true, ready = true, quotaFull = false, retained = fixtures
  return (method: string, path: string, query: URLSearchParams, body: Record<string, unknown>) => {
    // Preview scenarios are in-memory only; this endpoint is absent from production.
    if (method === 'POST' && path === '/api/v1/__preview/small-retention') {
      enabled = body.state !== 'empty'; ready = body.state !== 'not-ready'; quotaFull = body.state === 'quota-full'; retained = body.state === 'empty' ? [] : fixtures
      return { enabled, ready }
    }
    const base = '/api/v1/admin/request-captures'
    if (path === base + '/config' && method === 'GET') return { enabled: false, id1_diagnostic_enabled: false, diagnostic_user_ids: [], small_request_enabled: enabled, small_request_ready: ready, storage_ready: false, quota_mib: 1024, retention_days: 7 }
    if (path === base + '/small/config' && method === 'PUT') {
      if (typeof body.enabled !== 'boolean' || !ready) throw new Error('Invalid local preview switch')
      enabled = body.enabled
      return { enabled, ready }
    }
    if (path === base + '/small' && method === 'DELETE') { retained = []; return {} }
    if (method !== 'GET') throw new Error('Local preview write not supported')
    if (path === base + '/small/stats') {
      const records = retained.map(detail => detail.record)
      const requests = records.reduce((sum, record) => sum + record.repeat_count, 0)
      const raw = records.reduce((sum, record) => sum + record.raw_bytes, 0), stored = records.length * 1600
      const distribution: Record<string, number> = {}
      for (const record of records) { const bucket = record.repeat_count === 1 ? '1' : record.repeat_count < 10 ? '2-9' : record.repeat_count < 100 ? '10-99' : '100+'; distribution[bucket] = (distribution[bucket] || 0) + 1 }
      return { ready, records: records.length, requests, duplicates: requests - records.length, raw_bytes: raw, stored_bytes: stored, stored_limit_bytes: 4 * 1024 ** 3, quota_full: quotaFull, saved_bytes: raw - stored,
        oldest: records.length ? records[records.length - 1]!.first_seen : null, newest: records[0]?.last_seen || null, repeat_distribution: distribution,
        skipped: records.length ? { usage_unknown: 17, input_not_small: 39, response_too_large: 2, queue_full: 3 } : {} }
    }
    if (path === base + '/small') {
      const rows = retained.map(detail => detail.record).filter(record => {
        for (const field of ['user_id', 'api_key_id', 'status'] as const) if (query.get(field) && Number(query.get(field)) !== record[field]) return false
        if (query.get('model') && !record.model.includes(query.get('model')!)) return false
        if (query.get('request_id') && !record.request_ids.includes(query.get('request_id')!)) return false
        if (Number(query.get('min_repeats') || 0) > record.repeat_count) return false
        if (query.get('from') && record.last_seen < query.get('from')!) return false
        if (query.get('to') && record.last_seen > query.get('to')!) return false
        return true
      })
      const page = Math.max(1, Number(query.get('page') || 1)), size = Math.max(1, Math.min(100, Number(query.get('page_size') || 20)))
      return { items: rows.slice((page - 1) * size, page * size), has_more: page * size < rows.length }
    }
    const id = path.match(/\/small\/(\d+)$/)?.[1]
    if (id) { const detail = retained.find(detail => detail.record.id === Number(id)); if (detail) return detail }
    throw new Error('Local preview capture not found')
  }
}
