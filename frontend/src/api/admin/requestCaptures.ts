import { apiClient } from '../client'

export type CaptureTarget = 'user' | 'account' | 'group'
export interface CaptureTask {
  id: string; instance_id: string; target_type: CaptureTarget; target_id: number; target_name: string
  save_media: boolean; created_at: string; expires_at: string; ended_at?: string
  status: string; reason?: string; requests: number; partial: number; skipped: number; bytes: number
}
export interface CapturePart {
  name: string; stage: string; attempt: number; turn: number; content_type: string
  headers?: Record<string, string>; bytes: number; omitted?: string
}
export interface CaptureRecord {
  id: string; task_id: string; request_id: string; client_request_id?: string; instance_id: string
  user_id: number; group_id: number; routed_group_id?: number; model?: string; path: string
  protocol: string; status: number; is_error: boolean; turn?: number; partial: boolean; reason?: string
  created_at: string; finished_at?: string; bytes: number; error_code?: string; client_outcome?: 'completed' | 'failed'
  attempts: Array<{ number: number; account_id: number; status?: number; upstream_request_id?: string; error?: string; error_stage?: string; read_error?: string; response_terminal?: string; local_close?: boolean }>
  parts: CapturePart[]; usage?: Record<string, number>
}
export interface CaptureStats {
  instance_id: string; used_bytes: number; buffer_bytes: number; peak_buffer_bytes: number
  active_requests: number; admission_skipped: number; storage_error: boolean
}
const base = '/admin/request-captures'
export async function listTasks(page = 1) {
  return (await apiClient.get<{ items: CaptureTask[]; stats: CaptureStats; has_more: boolean }>(base, { params: { page, page_size: 20 } })).data
}
export async function createTask(input: { target_type: CaptureTarget; target_id: number; duration_minutes: number; save_media: boolean }) {
  return (await apiClient.post<CaptureTask>(base, input)).data
}
export async function stopTask(id: string) { await apiClient.post(base + '/' + id + '/stop') }
export async function deleteTask(id: string) { await apiClient.delete(base + '/' + id) }
export async function listRecords(task: string, page: number, requestID: string, errorsOnly: boolean) {
  return (await apiClient.get<{ items: CaptureRecord[]; has_more: boolean }>(base + '/' + task + '/requests', { params: { page, page_size: 20, request_id: requestID, errors_only: errorsOnly } })).data
}
export async function getRecord(task: string, record: string) {
  return (await apiClient.get<CaptureRecord>(base + '/' + task + '/requests/' + record)).data
}
export async function getContent(task: string, record: string, part: string, offset: number) {
  return (await apiClient.get<{ text: string; next_offset: number; has_more: boolean }>(base + '/' + task + '/requests/' + record + '/content/' + encodeURIComponent(part), { params: { offset } })).data
}

interface SaveHandle { createWritable(): Promise<WritableStream<Uint8Array>> }
type SaveWindow = Window & { showSaveFilePicker?: (options: { suggestedName: string }) => Promise<SaveHandle> }
export const canStreamExport = () => typeof (window as SaveWindow).showSaveFilePicker === 'function'
export async function exportCapture(task: string, record?: string) {
  const picker = (window as SaveWindow).showSaveFilePicker
  if (!picker) throw new Error('STREAM_SAVE_UNSUPPORTED')
  const handle = await picker.call(window, { suggestedName: 'request-capture-' + (record || task) + '.tar.gz' })
  const writable = await handle.createWritable()
  try {
    const path = base + '/' + task + (record ? '/requests/' + record : '') + '/export'
    const { data } = await apiClient.get<ReadableStream<Uint8Array>>(path, { adapter: 'fetch', responseType: 'stream', timeout: 0 })
    await data.pipeTo(writable)
  } catch (error) {
    try { await writable.abort(error) } catch { /* pipeTo may already have aborted the file */ }
    throw error
  }
}

export interface CaptureConfig {enabled:boolean;quota_mib:number;retention_days:number}
export async function getConfig(){return (await apiClient.get<CaptureConfig>(base+'/config')).data}
export async function saveConfig(input:CaptureConfig){return (await apiClient.put<CaptureConfig>(base+'/config',input)).data}
