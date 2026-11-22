import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

// 线上问题：夜间（暗色）主题下登录/注册页品牌文字看不见。根因是 AuthLayout
// 给站点名写死了浅色 #171717，又在 dark 下切成渐变 + text-transparent；副标题
// 与页脚用的 dark-400/500 也偏暗。修复改为 Nuxt UI 语义 token（light/dark 都会
// 解析成各自主题的可读前景色），不靠禁止选中来掩盖。
const dir = dirname(fileURLToPath(import.meta.url))
const layoutSource = readFileSync(resolve(dir, '../AuthLayout.vue'), 'utf8')
const quietStyles = readFileSync(resolve(dir, '../../../styles/auth-quiet.css'), 'utf8')

function rule(css: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return css.match(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`))?.[1] ?? ''
}

describe('AuthLayout brand text contrast', () => {
  it('paints the site name with the highlighted text token in both themes', () => {
    expect(layoutSource).toContain('<h1 class="text-2xl font-semibold text-highlighted">')
    // 旧的写死浅色 / 渐变透明实现必须消失，否则暗色下仍会发暗
    expect(layoutSource).not.toContain('#171717')
    expect(layoutSource).not.toContain('text-transparent')
    expect(layoutSource).not.toContain('text-gradient')
  })

  it('paints the subtitle with the muted text token', () => {
    expect(layoutSource).toContain('<p class="mt-1 text-sm text-muted">')
    expect(layoutSource).not.toContain('dark:text-dark-400')
  })

  it('keeps the legal footer and copyright on theme tokens too', () => {
    expect(layoutSource).not.toContain('dark:text-dark-500')
    expect(layoutSource).toContain('<div class="mt-5 text-muted">')
    expect(layoutSource).toContain('<div class="mt-8 text-center text-xs text-muted">')
  })

  it('does not mask the bug by disabling text selection', () => {
    expect(layoutSource).not.toContain('user-select')
    expect(layoutSource).not.toContain('-webkit-user-select')
  })

  it('keeps the auth card heading, sub and step label on theme tokens', () => {
    expect(rule(quietStyles, '.auth-heading')).toContain('color: var(--ui-text-highlighted)')
    expect(rule(quietStyles, '.auth-sub')).toContain('color: var(--ui-text-muted)')
    expect(rule(quietStyles, '.auth-step-label')).toContain('color: var(--ui-text)')
  })
})
