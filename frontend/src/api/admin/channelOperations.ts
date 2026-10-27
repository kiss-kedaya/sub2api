import { apiClient } from '../client'

export interface Upstream { id: number; name: string; members: number }
export interface Member { id: number; name: string; platform: string; status: string; schedulable: boolean; concurrency: number; upstream_id: number; eligible: boolean }
export interface Catalog { revision: number; upstreams: Upstream[]; upstream_total: number; accounts: Member[]; total: number }
export interface Mutation { action: 'create_upstream' | 'update_upstream' | 'delete_upstream' | 'assign' | 'detach'; revision: number; upstream_id?: number; account_id?: number; expected_upstream_id?: number; name?: string; confirm?: boolean }
export interface Metrics { requests: number; samples: number; unknown_samples: number; duration_samples: number; p50_ms: number | null; p90_ms: number | null; p95_ms: number | null; duration_p90_ms: number | null; latest_sample_at: string | null }
export interface UsageRow extends Metrics { dimension_id: number; name: string }
export interface Lineage { public_model: string; recorded_model: string; upstream_model: string | null; model_mapping_chain: string | null; samples: number }
export interface Usage { source: string; attribution: string; latency_measurement: string; summary: Metrics; total: number; rows: UsageRow[]; lineage: Lineage[]; lineage_total: number; error_rate: null; error_scope: string }
export interface QualityRound { id: number; account_id: number; plan_id: number; measured_model: string | null; observed_at: string; probes: number; passed_probes: number; expected: number | null; outcome: 'passed' | 'degraded' | 'inconclusive'; round_complete: boolean; round_probes: number; round_expected: number | null; actions: Array<string | null> }
export interface Quality { source: string; scope: string; summary: { rounds: number; passed: number; degraded: number; inconclusive: number; latest_sample_at: string | null }; rows: QualityRound[]; has_more: boolean }
export interface ErrorLogs { source: string; scope: string; records: number; upstream_5xx: number; latest_sample_at: string | null; rows: Array<{ error_type: string; error_phase: string; status_code: number; records: number }>; bucket_total: number; error_rate: null }
export interface Capacity { source: string; scope: string; accounts: number; schedulable: number; configured_concurrency: number }
export interface Section<T> { status: 'ready' | 'query_failed' | 'unsupported_scope'; data: T | null; error?: string; queried_at: string; stale_error?: string }
export interface Snapshot { scope: { channel_id: number; upstream_id: number; account_id: number; group_id: number; model: string; model_scope: string; dimension: string }; usage: Section<Usage>; quality: Section<Quality>; errors: Section<ErrorLogs>; capacity: Section<Capacity>; from: string; until: string; model_scope: string }
export interface CatalogQuery { page: number; page_size: number; search: string; upstream_page: number; upstream_search: string; upstream_id: number; membership: string }
export interface SnapshotQuery { channel_id: number; upstream_id: number; account_id: number; group_id: number; from: string; until: string; model: string; model_scope: string; dimension: string; page: number; page_size: number; quality_before: number }
export async function catalog(id: number, params: CatalogQuery, signal?: AbortSignal): Promise<Catalog> { return (await apiClient.get<Catalog>(`/admin/channels/${id}/operations`, { params, signal })).data }
export async function mutate(id: number, body: Mutation): Promise<{ revision: number }> { return (await apiClient.post<{ revision: number }>(`/admin/channels/${id}/operations`, body)).data }
export async function snapshot(params: SnapshotQuery, signal?: AbortSignal): Promise<Snapshot> { return (await apiClient.get<Snapshot>('/admin/channels/operations/snapshot', { params, signal })).data }
