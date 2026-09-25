import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import postcss from 'postcss'

const stylesheet = readFileSync(resolve(process.cwd(), 'src/styles/console-studio.css'), 'utf8')

describe('account workspace width', () => {
  it('opts the account workspace into the existing full-width page rule', () => {
    const root = postcss.parse(stylesheet)
    const rule = root.nodes.find((node) => node.type === 'rule' && node.selector === '.console-signal .signal-main:has(> .signal-usage, > .admin-usage-workbench, > .accounts-workspace)')
    expect(rule?.nodes.filter((node) => node.type === 'decl' && node.prop === 'max-width').map((node) => node.value)).toEqual(['none'])
  })

  it('keeps a reading-width limit for unrelated pages', () => {
    const root = postcss.parse(stylesheet)
    const rule = root.nodes.find((node) => node.type === 'rule' && node.selector === '.console-signal .signal-main')
    expect(rule?.nodes.filter((node) => node.type === 'decl' && node.prop === 'max-width').map((node) => node.value)).toContain('1680px')
  })
})
