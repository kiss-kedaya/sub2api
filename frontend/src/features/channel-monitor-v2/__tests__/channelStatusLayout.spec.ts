import { describe, expect, it } from 'vitest'
import { buildChannelStatusLayout } from '../channelStatusLayout'

describe('buildChannelStatusLayout', () => {
  it('keeps multi-group platforms as labeled clusters', () => {
    const blocks = buildChannelStatusLayout([
      { platform: 'openai', rows: [{ id: 1 }, { id: 2 }] },
      { platform: 'gemini', rows: [{ id: 3 }, { id: 4 }, { id: 5 }] },
    ])
    expect(blocks).toEqual([
      { kind: 'cluster', platform: 'openai', rows: [{ id: 1 }, { id: 2 }] },
      { kind: 'cluster', platform: 'gemini', rows: [{ id: 3 }, { id: 4 }, { id: 5 }] },
    ])
  })

  it('packs consecutive single-group platforms into one compact row', () => {
    const blocks = buildChannelStatusLayout([
      { platform: 'openai', rows: [{ id: 1 }, { id: 2 }, { id: 3 }, { id: 4 }] },
      { platform: 'anthropic', rows: [{ id: 5 }] },
      { platform: 'grok', rows: [{ id: 6 }] },
      { platform: 'gemini', rows: [{ id: 7 }, { id: 8 }] },
    ])
    expect(blocks.map(block => block.kind)).toEqual(['cluster', 'compact', 'cluster'])
    expect(blocks[1]).toEqual({
      kind: 'compact',
      items: [
        { platform: 'anthropic', row: { id: 5 } },
        { platform: 'grok', row: { id: 6 } },
      ],
    })
  })

  it('does not merge singles across a multi-group cluster', () => {
    const blocks = buildChannelStatusLayout([
      { platform: 'anthropic', rows: [{ id: 1 }] },
      { platform: 'openai', rows: [{ id: 2 }, { id: 3 }] },
      { platform: 'grok', rows: [{ id: 4 }] },
    ])
    expect(blocks.map(block => block.kind)).toEqual(['compact', 'cluster', 'compact'])
  })

  it('renders an all-singleton board as one compact grid', () => {
    const blocks = buildChannelStatusLayout([
      { platform: 'anthropic', rows: [{ id: 1 }] },
      { platform: 'grok', rows: [{ id: 2 }] },
      { platform: 'kimi', rows: [{ id: 3 }] },
    ])
    expect(blocks).toHaveLength(1)
    expect(blocks[0]?.kind).toBe('compact')
  })
})
