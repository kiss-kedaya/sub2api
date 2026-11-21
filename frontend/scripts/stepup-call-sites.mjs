#!/usr/bin/env node
/**
 * Step-up call-site scanner.
 *
 * Enumerates every admin API call site that may return a STEP_UP_REQUIRED
 * response: all write methods (POST/PUT/PATCH/DELETE) plus sensitive reads
 * (export / download / credential reveal / key material).
 *
 * The frontend now funnels every admin API request through the shared Axios
 * client, whose response interceptor opens the global TOTP step-up gate on a
 * 403 STEP_UP_* response. This scanner is the regression contract: the
 * checked-in baseline below freezes the current call-site list, so a new admin
 * write endpoint fails the test until the author reviews step-up handling and
 * regenerates the baseline.
 *
 * Regenerate the baseline:
 *   UPDATE_STEP_UP_BASELINE=1 pnpm vitest run src/api/__tests__/admin.stepUpCoverage.spec.ts
 *
 * Standalone report (used for the PR matrix):
 *   node scripts/stepup-call-sites.mjs [--json]
 */
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const FRONTEND_ROOT = resolve(HERE, '..')
const ADMIN_API_ROOT = join(FRONTEND_ROOT, 'src', 'api', 'admin')

/** GET endpoints whose response exposes secrets or bulk data (backend gates them too). */
const SENSITIVE_GET_PATTERNS = [
  /\/export(?:\/|$|\?)/,
  /\/download-url/,
  /\/credential(?:\/|$)/,
  /\/reveal(?:\/|$)/,
  /\/data(?:\/|$|\?)/,
  /\/api-keys(?:\/|$|\?)/,
  /\/sensitive-access/,
  /\/debug-dumps\//,
  /\/config(?:\/|$|\?)/,
]

const WRITE_METHODS = new Set(['post', 'put', 'patch', 'delete'])
const METHOD_NAMES = ['get', 'post', 'put', 'patch', 'delete']

function walk(dir, out = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === '__tests__' || entry.name.startsWith('.')) continue
    const full = join(dir, entry.name)
    if (entry.isDirectory()) walk(full, out)
    else if (entry.isFile() && entry.name.endsWith('.ts')) out.push(full)
  }
  return out
}

/** Human-readable name of the function/object method that owns a call site. */
function ownerNameOf(source, callIndex) {
  const before = source.slice(0, callIndex)
  const lines = before.split(/\r?\n/)
  for (let i = lines.length - 1; i >= 0; i -= 1) {
    const line = lines[i]
    let match = line.match(/^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+([A-Za-z0-9_$]+)/)
    if (match) return match[1]
    match = line.match(/^\s*(?:export\s+)?const\s+([A-Za-z0-9_$]+)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z0-9_$]+)\s*=>/)
    if (match) return match[1]
    // Object-literal method shorthand: `  async updateProvider(id, data) {`
    match = line.match(/^\s{2,}([A-Za-z0-9_$]+)\s*\([^)]*\)\s*\{/)
    if (match) return match[1]
  }
  return '<module>'
}

/** Collect top-level string constants used as API path prefixes (`const base = '/admin/...'`). */
function collectStringConsts(source) {
  const constants = new Map()
  const re = /^[ \t]*const[ \t]+([A-Za-z0-9_$]+)[ \t]*(?::[^=\n]+)?=[ \t]*(['"`])((?:(?!\2).)*)\2[ \t]*$/gm
  let match
  while ((match = re.exec(source)) !== null) constants.set(match[1], match[3])
  return constants
}

/** First argument of a call, resolving simple path constants and one string concat. */
function firstArgumentPath(rest, constants) {
  const literal = rest.match(/^\s*(['"`])((?:(?!\1).)*)\1/s)
  if (literal) return literal[2]
  const identifier = rest.match(/^\s*([A-Za-z0-9_$]+)/)
  if (!identifier) return ''
  const base = constants.get(identifier[1])
  if (!base) return ''
  const tail = rest.slice(identifier[0].length).match(/^\s*\+\s*(['"`])((?:(?!\1).)*)\1/)
  return base + (tail ? tail[2] : '')
}

function scanFile(file) {
  const source = readFileSync(file, 'utf8')
  const constants = collectStringConsts(source)
  const calls = []
  const pattern = new RegExp(
    `apiClient\\s*\\.\\s*(${METHOD_NAMES.join('|')})\\s*(?:<[^>]*>)?\\s*\\(`,
    'g',
  )
  let match
  while ((match = pattern.exec(source)) !== null) {
    const method = match[1].toLowerCase()
    // Extract the first string / template-literal / constant argument of the call.
    const rest = source.slice(pattern.lastIndex)
    const rawPath = firstArgumentPath(rest, constants)
    if (!rawPath.startsWith('/admin')) continue
    const owner = ownerNameOf(source, match.index)
    const entry = {
      file: relative(FRONTEND_ROOT, file).replace(/\\/g, '/'),
      owner,
      method: method.toUpperCase(),
      path: rawPath,
    }
    const isWrite = WRITE_METHODS.has(method)
    const isSensitiveRead = method === 'get' && SENSITIVE_GET_PATTERNS.some((re) => re.test(rawPath))
    if (!isWrite && !isSensitiveRead) continue
    calls.push(entry)
  }
  return calls
}

export function scanAdminCallSites() {
  const files = walk(ADMIN_API_ROOT).sort()
  return files.flatMap(scanFile).sort((a, b) => {
    const ka = `${a.file}|${a.owner}|${a.method}|${a.path}`
    const kb = `${b.file}|${b.owner}|${b.method}|${b.path}`
    return ka < kb ? -1 : ka > kb ? 1 : 0
  })
}

/** Serialize a call site to the stable line format stored in the baseline. */
export function serializeCallSite(entry) {
  return `${entry.method} ${entry.path} :: ${entry.file}#${entry.owner}`
}

export function serializeCallSites(entries = scanAdminCallSites()) {
  return entries.map(serializeCallSite)
}

export function baselinePath() {
  return join(ADMIN_API_ROOT, 'stepUpCallSites.baseline.json')
}

export function loadBaseline() {
  return JSON.parse(readFileSync(baselinePath(), 'utf8'))
}

// Standalone report mode.
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const entries = scanAdminCallSites()
  if (process.argv.includes('--json')) {
    process.stdout.write(JSON.stringify(entries, null, 2) + '\n')
  } else {
    for (const line of serializeCallSites(entries)) process.stdout.write(line + '\n')
    process.stdout.write(`\n${entries.length} step-up-relevant admin call sites\n`)
  }
}
