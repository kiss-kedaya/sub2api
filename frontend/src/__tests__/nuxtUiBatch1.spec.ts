import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * Nuxt UI 迁移批次守卫（batch 1）。
 *
 * 老板要求「能换成 Nuxt UI 的地方都换成 Nuxt UI 组件」。一次性全量重构风险高，
 * 所以按批次推进；这个测试把每一批的「已完成范围」固定下来：
 * 文件进了这个清单，就必须彻底迁移完——源码里不允许再出现手写按钮 / 输入框 /
 * 开关 / 徽章 / tooltip，也不允许再套旧的 `.btn` / `.input` / `.badge` 工具类。
 *
 * 新增批次时只追加 BATCH1_SCOPE 以外的常量并复制断言，不要改动已完成的条目：
 * 已迁移文件一旦回退成手写元素，这里会直接失败。
 */
const frontendRoot = resolve(__dirname, '../..')

/** 每个文件至少使用的 Nuxt UI 组件（正向断言，防止文件被误删成空壳）。 */
const NUXT_UI_SCOPE: Array<{ path: string; components: string[] }> = [
  { path: 'src/components/user/profile/ProfileEditForm.vue', components: ['<UInput', '<UButton'] },
  { path: 'src/components/user/profile/ProfilePasswordForm.vue', components: ['<UInput', '<UButton'] },
  { path: 'src/components/user/profile/ProfileBarkCard.vue', components: ['<USwitch', '<UInput', '<UButton'] },
  { path: 'src/components/user/profile/ProfileBalanceNotifyCard.vue', components: ['<USwitch', '<UInput', '<UButton'] },
  { path: 'src/components/user/profile/ProfileIdentityBindingsSection.vue', components: ['<UBadge', '<UInput', '<UButton'] },
  { path: 'src/components/user/profile/ProfilePasskeyCard.vue', components: ['<UBadge', '<UInput', '<UButton'] },
  { path: 'src/components/user/profile/ProfileTotpCard.vue', components: ['<UButton'] },
  { path: 'src/components/admin/settings/RegistrationEmailBlacklistEditor.vue', components: ['<UBadge', '<UTextarea', '<UButton'] },
  { path: 'src/components/channels/SupportedModelChip.vue', components: ['<UTooltip'] },
]

/** 手写 UI 的源码特征：「出现即失败」。 */
const HANDWRITTEN_UI: Array<{ label: string; pattern: RegExp }> = [
  { label: '手写 <button>', pattern: /<button[\s>]/ },
  { label: '手写 <input>', pattern: /<input[\s>]/ },
  { label: '手写 <select>', pattern: /<select[\s>]/ },
  { label: '手写 <textarea>', pattern: /<textarea[\s>]/ },
  { label: '手写开关（peer-checked）', pattern: /peer-checked:/ },
  { label: '手写开关/勾选（role 属性）', pattern: /role="(?:switch|checkbox|radio)"/ },
  { label: '手写 tooltip（role="tooltip"）', pattern: /role="tooltip"/ },
  { label: '旧 .btn 工具类', pattern: /class="btn(?:"|\s)/ },
  { label: '旧 .input 工具类', pattern: /class="input(?:"|\s)/ },
  { label: '旧 .badge 工具类', pattern: /class="badge(?:"|\s)/ },
]

function readFrontendFile(path: string): string {
  return readFileSync(resolve(frontendRoot, path), 'utf8')
}

describe('Nuxt UI 迁移 batch1 范围守卫', () => {
  it.each(NUXT_UI_SCOPE)('$path 已全部换成 Nuxt UI 组件，无手写控件残留', ({ path, components }) => {
    const source = readFrontendFile(path)

    for (const { label, pattern } of HANDWRITTEN_UI) {
      expect(source, `${path} 仍残留${label}`).not.toMatch(pattern)
    }
    for (const component of components) {
      expect(source, `${path} 缺少预期的 ${component}>`).toContain(component)
    }
  })

  it('batch1 清单覆盖用户侧设置与管理端设置，且每条都有断言', () => {
    expect(NUXT_UI_SCOPE.length).toBeGreaterThanOrEqual(9)

    const userSide = NUXT_UI_SCOPE.filter(({ path }) => path.includes('/user/'))
    const adminSide = NUXT_UI_SCOPE.filter(({ path }) => path.includes('/admin/'))
    expect(userSide.length).toBeGreaterThan(0)
    expect(adminSide.length).toBeGreaterThan(0)
  })

  it('夜间主题给 USwitch 旋钮单独定义反色，关闭态在墨色轨道上不丢对比', () => {
    const css = readFrontendFile('src/styles/nuxt-ui-theme.css')

    expect(css).toMatch(/\.dark\s+\[role='switch'\]\s+\[data-slot='thumb'\]/)
    expect(css).toMatch(/\[data-slot='thumb'\]\s*\{[^}]*var\(--ui-bg-inverted\)/)
  })
})
