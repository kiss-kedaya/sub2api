#!/usr/bin/env node
/**
 * Frontend build manifest: one file that names the exact frontend build embedded
 * in (or served next to) a Go binary. It answers "which commit produced this UI?"
 * for both the legacy Vite SPA and the Nuxt/Nitro SSR bundle.
 *
 *   node scripts/build-manifest.mjs emit   --dir <outDir> --mode spa|ssr
 *   node scripts/build-manifest.mjs verify --dir <outDir> [--binary <file>]
 *
 * The manifest is deterministic: `manifestHash` covers version, commit, mode and
 * every file hash/size, but not `builtAt`, so rebuilding the same sources yields
 * the same hash.
 */
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { dirname, join, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

export const MANIFEST_NAME = 'frontend-build.json'
export const MANIFEST_SCHEMA = 1
export const PRODUCT = 'sub2api-frontend'

const HERE = dirname(fileURLToPath(import.meta.url))
export const REPO_ROOT = resolve(HERE, '../..')
const MODES = new Set(['spa', 'ssr'])

function sha256(buffer) {
  return createHash('sha256').update(buffer).digest('hex')
}

function git(args) {
  try {
    return execFileSync('git', args, { cwd: REPO_ROOT, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim()
  } catch {
    return ''
  }
}

function readRepoVersion() {
  const file = join(REPO_ROOT, 'backend/cmd/server/VERSION')
  if (!existsSync(file)) return '0.0.0'
  return readFileSync(file, 'utf8').replace(/\s+/g, '') || '0.0.0'
}

function resolveVersion(explicit) {
  if (explicit) return String(explicit).replace(/^v/, '')
  // Container builds have no .git and no backend/ tree next to frontend/.
  if (process.env.FRONTEND_BUILD_VERSION) return String(process.env.FRONTEND_BUILD_VERSION).replace(/^v/, '')
  const tag = git(['describe', '--tags', '--exact-match', '--match', 'v[0-9]*'])
    || git(['describe', '--tags', '--exact-match', '--match', '[0-9]*'])
  return (tag || readRepoVersion()).replace(/^v/, '')
}

function resolveCommit(explicit) {
  return String(explicit || process.env.FRONTEND_BUILD_COMMIT || git(['rev-parse', 'HEAD']) || 'unknown')
}

function isDirty() {
  const status = git(['status', '--porcelain', '--untracked-files=normal'])
  return status.length > 0
}

function walk(root, current = root, files = []) {
  for (const entry of readdirSync(current).sort()) {
    const full = join(current, entry)
    const info = statSync(full)
    if (info.isDirectory()) walk(root, full, files)
    else if (info.isFile()) files.push(relative(root, full).split(sep).join('/'))
  }
  return files
}

/** Stable JSON: object keys sorted at every level, so hashes never depend on insertion order. */
export function canonicalJson(value) {
  const normalize = (input) => {
    if (Array.isArray(input)) return input.map(normalize)
    if (input && typeof input === 'object') {
      const out = {}
      for (const key of Object.keys(input).sort()) out[key] = normalize(input[key])
      return out
    }
    return input
  }
  return JSON.stringify(normalize(value))
}

function buildCanonical(manifest) {
  return canonicalJson({
    schema: manifest.schema,
    product: manifest.product,
    mode: manifest.mode,
    version: manifest.version,
    commit: manifest.commit,
    entry: manifest.entry,
    files: manifest.files,
  })
}

export function createManifest({ dir, mode, version, commit }) {
  if (!MODES.has(mode)) throw new Error(`unknown mode "${mode}" (expected spa|ssr)`)
  const root = resolve(dir)
  const entry = existsSync(join(root, 'index.html')) ? 'index.html' : ''
  const files = walk(root)
    .filter((path) => path !== MANIFEST_NAME)
    .map((path) => {
      const content = readFileSync(join(root, path))
      return { path, sha256: sha256(content), size: content.length }
    })
  const manifest = {
    schema: MANIFEST_SCHEMA,
    product: PRODUCT,
    mode,
    version: resolveVersion(version),
    commit: resolveCommit(commit),
    dirty: isDirty(),
    node: process.version,
    builtAt: new Date().toISOString(),
    entry,
    files,
  }
  manifest.manifestHash = sha256(buildCanonical(manifest))
  return manifest
}

export function readManifest(dir) {
  const file = join(resolve(dir), MANIFEST_NAME)
  if (!existsSync(file)) throw new Error(`missing ${file}`)
  return JSON.parse(readFileSync(file, 'utf8'))
}

export function verifyManifest(dir, options = {}) {
  const root = resolve(dir)
  const errors = []
  const warnings = []
  let manifest
  try {
    manifest = readManifest(root)
  } catch (error) {
    return { errors: [String(error.message || error)], warnings, manifest: null }
  }

  if (manifest.schema !== MANIFEST_SCHEMA) errors.push(`schema ${manifest.schema} != ${MANIFEST_SCHEMA}`)
  if (manifest.product !== PRODUCT) errors.push(`product "${manifest.product}" != "${PRODUCT}"`)
  if (options.mode && manifest.mode !== options.mode) errors.push(`mode "${manifest.mode}" != "${options.mode}"`)
  if (options.expectCommit && manifest.commit !== options.expectCommit) {
    errors.push(`commit ${manifest.commit} != expected ${options.expectCommit}`)
  }
  if (options.expectVersion && manifest.version !== String(options.expectVersion).replace(/^v/, '')) {
    errors.push(`version ${manifest.version} != expected ${options.expectVersion}`)
  }
  if (!options.allowCommitDrift) {
    const head = git(['rev-parse', 'HEAD'])
    if (head && manifest.commit !== head) errors.push(`manifest commit ${manifest.commit} != checkout HEAD ${head}`)
  }

  const expectedHash = sha256(buildCanonical(manifest))
  if (expectedHash !== manifest.manifestHash) {
    errors.push(`manifestHash mismatch (stored ${manifest.manifestHash}, computed ${expectedHash})`)
  }

  const listed = new Set()
  for (const file of Array.isArray(manifest.files) ? manifest.files : []) {
    listed.add(file.path)
    const full = join(root, file.path)
    if (!existsSync(full)) {
      errors.push(`missing file: ${file.path}`)
      continue
    }
    const content = readFileSync(full)
    const digest = sha256(content)
    if (digest !== file.sha256) errors.push(`hash mismatch: ${file.path}`)
    if (content.length !== file.size) errors.push(`size mismatch: ${file.path}`)
  }
  for (const path of walk(root)) {
    if (path === MANIFEST_NAME || listed.has(path)) continue
    const message = `unlisted file in build output: ${path}`
    if (options.strict) errors.push(message)
    else warnings.push(message)
  }

  if (options.binary) {
    if (!existsSync(options.binary)) {
      errors.push(`binary not found: ${options.binary}`)
    } else {
      const binary = readFileSync(options.binary)
      if (!binary.includes(Buffer.from(manifest.manifestHash))) {
        errors.push(`binary does not embed manifestHash ${manifest.manifestHash} (built without -tags=embed or from another dist?)`)
      }
      const commitMarkers = [`"commit": "${manifest.commit}"`, `"commit":"${manifest.commit}"`]
      if (!commitMarkers.some((marker) => binary.includes(Buffer.from(marker)))) {
        errors.push(`binary does not embed commit ${manifest.commit}`)
      }
    }
  }

  return { errors, warnings, manifest }
}

function parseArgs(argv) {
  const args = { _: [] }
  for (let index = 0; index < argv.length; index += 1) {
    const token = argv[index]
    if (token.startsWith('--')) {
      const key = token.slice(2)
      const next = argv[index + 1]
      if (!next || next.startsWith('--')) args[key] = true
      else {
        args[key] = next
        index += 1
      }
    } else {
      args._.push(token)
    }
  }
  return args
}

function printResult(result, label) {
  for (const warning of result.warnings) console.warn(`warn: ${warning}`)
  for (const error of result.errors) console.error(`error: ${error}`)
  const manifest = result.manifest
  if (manifest) {
    console.log(
      `${label}: mode=${manifest.mode} version=${manifest.version} commit=${manifest.commit.slice(0, 12)} ` +
      `hash=${manifest.manifestHash.slice(0, 16)} files=${manifest.files.length} dirty=${manifest.dirty}`,
    )
  }
  return result.errors.length === 0
}

function runCli() {
  const args = parseArgs(process.argv.slice(2))
  const command = args._[0]
  if (!command || command === 'help' || args.help) {
    console.log('usage: build-manifest.mjs <emit|verify> --dir <dir> [--mode spa|ssr] [--binary <file>] [--expect-commit <sha>] [--strict] [--copy-to <file>]')
    process.exit(command ? 0 : 2)
  }

  if (command === 'emit') {
    if (!args.dir) throw new Error('emit requires --dir')
    if (!args.mode) throw new Error('emit requires --mode spa|ssr')
    const manifest = createManifest({ dir: args.dir, mode: args.mode, version: args.version, commit: args.commit })
    const target = join(resolve(args.dir), MANIFEST_NAME)
    writeFileSync(target, `${JSON.stringify(manifest, null, 2)}\n`)
    if (args['copy-to']) {
      const copyTo = resolve(args['copy-to'])
      mkdirSync(dirname(copyTo), { recursive: true })
      copyFileSync(target, copyTo)
    }
    printResult({ errors: [], warnings: [], manifest }, 'manifest emitted')
    if (manifest.dirty) console.warn('warn: build produced from a dirty checkout')
    return
  }

  if (command === 'verify') {
    if (!args.dir) throw new Error('verify requires --dir')
    const result = verifyManifest(args.dir, {
      mode: args.mode,
      binary: args.binary,
      expectCommit: args['expect-commit'],
      expectVersion: args['expect-version'],
      allowCommitDrift: Boolean(args['allow-commit-drift']),
      strict: Boolean(args.strict),
    })
    const ok = printResult(result, 'manifest verified')
    if (!ok) process.exit(1)
    return
  }

  throw new Error(`unknown command "${command}"`)
}

const invokedDirectly = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)
if (invokedDirectly) {
  try {
    runCli()
  } catch (error) {
    console.error(`error: ${error.message || error}`)
    process.exit(1)
  }
}
