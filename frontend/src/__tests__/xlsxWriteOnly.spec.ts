import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

// SheetJS is pinned to the patched official release. Keep the application
// export-only and lazily loaded so new upload/parsing surfaces require review.
const SRC_DIR = join(process.cwd(), 'src')

// 允许的 xlsx API —— 全部是构造/写出路径，不解析外部输入。
const ALLOWED_CALLS = [
  'utils.aoa_to_sheet',
  'utils.sheet_add_aoa',
  'utils.book_new',
  'utils.book_append_sheet',
  'write'
]

// Parsing external workbooks is outside the application's export feature.
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
    // Test fixtures may read back an internally generated workbook; only scan
    // production source for accidental workbook import/parsing features.
    if (/\.(spec|test)\.[cm]?[jt]sx?$/.test(entry)) continue
    out.push(full)
  }
  return out
}

describe('xlsx stays an isolated export-only dependency', () => {
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
    expect(offenders, 'production workbook parsing requires a separate security review').toEqual([])
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
    expect(offenders, 'unexpected xlsx API outside the export feature').toEqual([])
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
