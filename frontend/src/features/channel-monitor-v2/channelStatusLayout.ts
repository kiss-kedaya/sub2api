export type ChannelStatusPlatformSection<T> = {
  platform: string
  rows: T[]
}

export type ChannelStatusLayoutBlock<T> =
  | { kind: 'cluster'; platform: string; rows: T[] }
  | { kind: 'compact'; items: Array<{ platform: string; row: T }> }

// Platforms with 2+ groups keep a labeled cluster. Consecutive single-group
// platforms share one grid row so they do not each occupy a full-width band.
export function buildChannelStatusLayout<T>(
  sections: ChannelStatusPlatformSection<T>[],
): ChannelStatusLayoutBlock<T>[] {
  const blocks: ChannelStatusLayoutBlock<T>[] = []
  let compact: Array<{ platform: string; row: T }> = []

  const flushCompact = () => {
    if (compact.length === 0) return
    blocks.push({ kind: 'compact', items: compact })
    compact = []
  }

  for (const section of sections) {
    if (section.rows.length >= 2) {
      flushCompact()
      blocks.push({ kind: 'cluster', platform: section.platform, rows: section.rows })
      continue
    }
    if (section.rows.length === 1) {
      compact.push({ platform: section.platform, row: section.rows[0] })
    }
  }
  flushCompact()
  return blocks
}

export function channelStatusLayoutBlockKey<T>(block: ChannelStatusLayoutBlock<T>, index: number): string {
  if (block.kind === 'cluster') return `cluster:${block.platform}`
  return `compact:${index}:${block.items.map(item => item.platform).join('|')}`
}
