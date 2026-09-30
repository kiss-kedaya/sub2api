import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * xlsx 的豁免不能只是一句注释。
 *
 * `.github/audit-exceptions.yml` 为 xlsx 的两条 high 漏洞（GHSA-4r6h-8v6p-xvw6
 * 原型污染、GHSA-5pgg-2g8v-p4x9 ReDoS）做了豁免，理由是「仅管理端导出、已改为
 * 动态导入」。**这两条漏洞都在解析路径上**——原型污染出自 `XLSX.read`
 * 对不可信工作簿的处理，ReDoS 出自解析恶意文件。
 *
 * 本项目对 xlsx 的用法是**纯写**：aoa_to_sheet / sheet_add_aoa / book_new /
 * book_append_sheet / write，一个解析 API 都没调用，所以漏洞不可达。
 *
 * 但这个「不可达」是个**会悄悄失效的前提**：只要以后有人写一句
 * `XLSX.read(用户上传的文件)`，豁免立刻从「合理」变成「错误」，而审计清单
 * 不会知道。所以把它变成一条会在 CI 里失败的测试。
 */
const SRC_DIR = join(process.cwd(), 'src')

// 允许的 xlsx API —— 全部是构造/写出路径，不解析外部输入。
const ALLOWED_CALLS = [
  'utils.aoa_to_sheet',
  'utils.sheet_add_aoa',
  'utils.book_new',
  'utils.book_append_sheet',
  'write'
]

// 禁止的解析 API —— 一旦出现，豁免前提即失效。
const FORBIDDEN_PATTERNS: Array<{ pattern: RegExp; what: string }> = [
  { pattern: /\bXLSX\.read(File)?(Sync)?\s*\(/, what: 'XLSX.read/readFile' },
  { pattern: /\bXLSX\.utils\.sheet_to_json\s*\(/, what: 'sheet_to_json' },
  { pattern: /\bXLSX\.parse\s*\(/, what: 'XLSX.parse' },
  { pattern: /\.readFileSync\s*\([^)]*xlsx/i, what: 'readFileSync on an xlsx path' }
]

function collectSourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      collectSourceFiles(full, out)
      continue
    }
    if (!/\.(ts|vue|js|mts|cts)$/.test(entry)) continue
    // 本文件自己的正则里就写着那些禁用模式，必须排除，否则测试会抓到自己。
    if (entry === 'xlsxWriteOnly.spec.ts') continue
    out.push(full)
  }
  return out
}

describe('xlsx is write-only (the premise behind its audit exception)', () => {
  const files = collectSourceFiles(SRC_DIR)

  it('never calls an xlsx parsing API anywhere in src', () => {
    const offenders: string[] = []
    for (const file of files) {
      const text = readFileSync(file, 'utf8')
      if (!text.includes('xlsx')) continue
      for (const { pattern, what } of FORBIDDEN_PATTERNS) {
        if (pattern.test(text)) {
          offenders.push(`${relative(process.cwd(), file)}: ${what}`)
        }
      }
    }
    expect(offenders, 'xlsx parsing is reachable — the audit exception is no longer valid').toEqual([])
  })

  it('only uses the allowed write-path API surface', () => {
    const offenders: string[] = []
    for (const file of files) {
      const text = readFileSync(file, 'utf8')
      if (!text.includes('XLSX')) continue
      const calls = text.match(/\bXLSX\.[A-Za-z_][A-Za-z0-9_.]*/g) ?? []
      for (const call of calls) {
        const name = call.replace(/^XLSX\./, '')
        if (!ALLOWED_CALLS.includes(name)) {
          offenders.push(`${relative(process.cwd(), file)}: ${call}`)
        }
      }
    }
    expect(offenders, 'unexpected xlsx API — review whether the audit exception still holds').toEqual([])
  })

  it('loads xlsx lazily so it never enters the initial bundle', () => {
    const eager = files.filter((file) => {
      const text = readFileSync(file, 'utf8')
      // 只允许 `await import('xlsx')` 这种动态导入。
      return /^\s*import\s+.*\bfrom\s+['"]xlsx['"]/m.test(text)
    })
    expect(eager.map((f) => relative(process.cwd(), f))).toEqual([])
  })
})
