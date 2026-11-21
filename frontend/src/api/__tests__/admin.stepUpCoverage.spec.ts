/**
 * Step-up coverage contract.
 *
 * The 2026-11 admin incident: "Edit payment provider → Save" surfaced the raw
 * English backend message "This operation requires recent two-factor
 * verification" instead of a TOTP dialog. Root cause: the page called the
 * admin API directly and ignored the 403 STEP_UP_REQUIRED response.
 *
 * Fix/architecture: every admin API request flows through the shared Axios
 * client (src/api/client.ts). Its response interceptor now opens the app-wide
 * TOTP gate and retries the request once after verification, so call sites do
 * not need per-page retry logic. These tests freeze that invariant:
 *
 *  1. A checked-in baseline lists every step-up-relevant admin call site
 *     (writes + sensitive reads). Adding/removing one fails until the author
 *     reviews step-up handling and refreshes the baseline:
 *       UPDATE_STEP_UP_BASELINE=1 pnpm vitest run src/api/__tests__/admin.stepUpCoverage.spec.ts
 *  2. Admin API modules must use the shared guarded client — never a raw axios
 *     instance or fetch — otherwise the gate cannot see their 403s.
 *  3. The global gate must stay wired: App.vue mounts <StepUpGate /> and
 *     client.ts routes STEP_UP_* rejections through it.
 */
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import {
  baselinePath,
  loadBaseline,
  scanAdminCallSites,
  serializeCallSites,
} from '../../../scripts/stepup-call-sites.mjs'

// vitest runs with the frontend package as cwd (pnpm test:run / make test-frontend);
// fall back to the nested directory when invoked from the repo root.
const ROOT = existsSync(join(process.cwd(), 'src', 'App.vue'))
  ? process.cwd()
  : join(process.cwd(), 'frontend')
const read = (relativePath: string) => readFileSync(join(ROOT, relativePath), 'utf8')

describe('admin step-up call-site coverage', () => {
  it('keeps the reviewed step-up call-site baseline in sync', () => {
    const actual = serializeCallSites(scanAdminCallSites())
    if (process.env.UPDATE_STEP_UP_BASELINE === '1') {
      writeFileSync(baselinePath(), JSON.stringify({ version: 1, sites: actual }, null, 2) + '\n')
      return
    }
    const baseline = loadBaseline() as { version: number; sites: string[] }
    const expected = baseline.sites
    const added = actual.filter((site) => !expected.includes(site))
    const removed = expected.filter((site) => !actual.includes(site))
    const report = [
      added.length ? `新增调用点（请确认已受统一 step-up 门保护后刷新基线）:\n  ${added.join('\n  ')}` : '',
      removed.length ? `删除/改写的调用点（同步刷新基线）:\n  ${removed.join('\n  ')}` : '',
    ]
      .filter(Boolean)
      .join('\n')
    expect(
      { added, removed },
      `管理端 step-up 调用点清单发生变化。\n${report}\n\n` +
        '统一门禁在 src/api/client.ts 的响应拦截器里，任何走 apiClient 的调用都会自动弹 TOTP 并重试一次；' +
        '刷新基线：UPDATE_STEP_UP_BASELINE=1 pnpm vitest run src/api/__tests__/admin.stepUpCoverage.spec.ts',
    ).toEqual({ added: [], removed: [] })
  })

  it('routes every admin API module through the shared guarded client', () => {
    const modules = [...new Set(scanAdminCallSites().map((site) => site.file))]
    expect(modules.length).toBeGreaterThan(20)
    const offenders: string[] = []
    for (const file of modules) {
      const source = read(file)
      if (!/from\s+['"](?:\.\.?\/(?:client|\.\/client)|@\/api\/client)['"]/.test(source)) {
        offenders.push(`${file}: 未使用共享 apiClient`)
      }
      if (/axios\.create\s*\(/.test(source)) offenders.push(`${file}: 自建 axios 实例绕过统一门禁`)
      if (/\bfetch\s*\(/.test(source)) offenders.push(`${file}: 直接使用 fetch 绕过统一门禁`)
    }
    expect(offenders, '管理端 API 必须走 src/api/client.ts，否则 step-up 403 不会被统一拦截').toEqual([])
  })

  it('keeps the global TOTP gate wired into the app shell and client', () => {
    const app = read('src/App.vue')
    expect(app).toContain('StepUpGate')
    const client = read('src/api/client.ts')
    expect(client).toContain('retryWithGlobalStepUp')
    expect(client).toContain('stepUpMarkerOf')
    const gate = read('src/components/auth/StepUpGate.vue')
    expect(gate).toContain('registerStepUpHost')
    expect(gate).toContain('TotpStepUpDialog')
  })

  it('has no admin view bypassing the client with raw fetch/axios', () => {
    const targets = ['src/views/admin', 'src/components/admin']
    const offenders: string[] = []
    const walk = (relativeDir: string) => {
      for (const entry of readdirSync(join(ROOT, relativeDir), { withFileTypes: true })) {
        const child = `${relativeDir}/${entry.name}`
        if (entry.isDirectory()) {
          if (entry.name !== '__tests__') walk(child)
        } else if (/\.(vue|ts)$/.test(entry.name)) {
          const source = read(child)
          const rawAdminFetch = /\bfetch\s*\(\s*[`'"]\/?api\/v\d+\/admin/.test(source)
          if (rawAdminFetch || /axios\.create\s*\(/.test(source)) offenders.push(child)
        }
      }
    }
    for (const target of targets) walk(target)
    expect(offenders, '管理端页面不得绕过统一 apiClient 直接请求 admin 接口').toEqual([])
  })
})
