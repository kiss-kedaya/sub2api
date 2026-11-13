import { describe, expect, it } from 'vitest'
import type { MonitorMatrixBucket, MonitorMatrixRow } from '@/api/channelMonitorV2'
import { bucketStatusClass, groupStatusClass, latestBucket } from '../groupStatusDot'

function bucket(start: string, errorRate: number, overall: 'healthy' | 'warning' | 'critical' | 'unknown'): MonitorMatrixBucket {
  return {
    bucket_start: start,
    // Only the fields these helpers read are populated; the rest of the metric
    // payload is irrelevant to colouring a bar.
    metrics: { error_rate: errorRate } as MonitorMatrixBucket['metrics'],
    health: { overall } as MonitorMatrixBucket['health'],
  }
}

function row(buckets: MonitorMatrixBucket[]): MonitorMatrixRow {
  return {
    platform: 'openai',
    group_id: 1,
    metrics: {} as MonitorMatrixRow['metrics'],
    health: { overall: 'healthy' } as MonitorMatrixRow['health'],
    buckets,
  }
}

describe('groupStatusDot', () => {
  it('colours a known bucket by availability, exactly like the monitor bar', () => {
    expect(bucketStatusClass(bucket('2026-01-01T00:00:00Z', 0, 'healthy'))).toBe('bg-emerald-600 dark:bg-emerald-400')
    // The monitor's bands: 45% error -> 55% availability -> amber, 60% -> red.
    expect(bucketStatusClass(bucket('2026-01-01T00:00:00Z', 0.45, 'warning'))).toBe('bg-amber-400 dark:bg-amber-300')
    expect(bucketStatusClass(bucket('2026-01-01T00:00:00Z', 0.6, 'critical'))).toBe('bg-red-500 dark:bg-red-400')
  })

  it('treats an unknown or missing bucket as grey, never as healthy', () => {
    expect(bucketStatusClass(bucket('2026-01-01T00:00:00Z', 0, 'unknown'))).toBe('bg-gray-300 dark:bg-dark-600')
    expect(bucketStatusClass(null)).toBe('bg-gray-300 dark:bg-dark-600')
    expect(bucketStatusClass(undefined)).toBe('bg-gray-300 dark:bg-dark-600')
  })

  it('uses the newest bucket regardless of the order the API returned', () => {
    const r = row([
      bucket('2026-01-01T03:00:00Z', 0.6, 'critical'),
      bucket('2026-01-01T01:00:00Z', 0, 'healthy'),
      bucket('2026-01-01T02:00:00Z', 0, 'healthy'),
    ])
    expect(latestBucket(r)?.bucket_start).toBe('2026-01-01T03:00:00Z')
    expect(groupStatusClass(r)).toBe('bg-red-500 dark:bg-red-400')
  })

  it('reports nothing for a group the monitor has no buckets for', () => {
    expect(groupStatusClass(row([]))).toBeNull()
    expect(groupStatusClass(null)).toBeNull()
  })
})
