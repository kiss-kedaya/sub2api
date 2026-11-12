import type { UpstreamKindKey } from './upstreamBalance'

// Lucide paths copied locally on purpose: the badge used to resolve `i-lucide-*` at
// runtime, and an unresolvable name silently rendered an empty <svg> (no paths at all)
// instead of failing. Keeping the geometry here means the icon always draws.
export type UpstreamKindShape = { d: string } | { cx: number, cy: number, r: number }

export const UPSTREAM_KIND_SHAPES: Record<UpstreamKindKey, UpstreamKindShape[]> = {
  sub2api: [
    { d: 'M12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83z' },
    { d: 'M2 12a1 1 0 0 0 .58.91l8.6 3.91a2 2 0 0 0 1.65 0l8.58-3.9A1 1 0 0 0 22 12' },
    { d: 'M2 17a1 1 0 0 0 .58.91l8.6 3.91a2 2 0 0 0 1.65 0l8.58-3.9A1 1 0 0 0 22 17' }
  ],
  newapi: [
    { d: 'M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z' },
    { d: 'm3.3 7l8.7 5l8.7-5M12 22V12' }
  ],
  openai_compatible: [
    { d: 'M12 22v-5m3-9V2m2 6a1 1 0 0 1 1 1v4a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V9a1 1 0 0 1 1-1zM9 8V2' }
  ],
  official: [
    { d: 'M3.85 8.62a4 4 0 0 1 4.78-4.77a4 4 0 0 1 6.74 0a4 4 0 0 1 4.78 4.78a4 4 0 0 1 0 6.74a4 4 0 0 1-4.77 4.78a4 4 0 0 1-6.75 0a4 4 0 0 1-4.78-4.77a4 4 0 0 1 0-6.76' },
    { d: 'm16 9l-5.5 5.5L8 12' }
  ],
  custom: [
    { d: 'M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.106-3.105c.32-.322.863-.22.983.218a6 6 0 0 1-8.259 7.057l-7.91 7.91a1 1 0 0 1-2.999-3l7.91-7.91a6 6 0 0 1 7.057-8.259c.438.12.54.662.219.984z' }
  ],
  unknown: [
    { cx: 12, cy: 12, r: 10 },
    { d: 'M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3m.08 4h.01' }
  ]
}
