#!/usr/bin/env node
/**
 * Smoke test for the *packaged* SSR artifact.
 *
 * CI builds on one machine and operators unpack on another, so the only thing
 * worth testing is the tarball that is about to be uploaded: unpack it into a
 * throwaway directory and boot that copy.
 *
 *   node deploy/nuxt/smoke-ssr.mjs --dir /tmp/ssr-extract/ssr
 *
 * Checks, in order:
 *   1. the extracted tree contains no symlink at all (build-workspace links such
 *      as `/home/runner/work/.../node_modules/.pnpm/...` are dead everywhere
 *      else and make Nitro die with ERR_MODULE_NOT_FOUND on the first render);
 *   2. the running Node matches the one recorded in the sealed release;
 *   3. SHA256SUMS still matches every packaged file;
 *   4. no file still names the throwaway CI workspace;
 *   5. `node .output/server/index.mjs` answers `/health/frontend?live=1` with 200
 *      and renders `/` with 200, following same-origin redirects (production
 *      sends an unauthenticated `/` to `/login`, and a bare 302 is not a pass);
 *   6. the client bundle referenced by that HTML is actually inside the archive.
 */
import { spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, readFileSync, readdirSync, realpathSync, statSync } from 'node:fs'
import { createServer } from 'node:net'
import { join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { once } from 'node:events'
import { scanSymlinks } from './release.mjs'

const DEFAULT_TIMEOUT_MS = 90000
const MAX_REPORTED_LINKS = 20
const MAX_REDIRECTS = 5
const REDIRECT_STATUS = new Set([301, 302, 303, 307, 308])
// Anything below these roots is the throwaway CI workspace: dead on a server and
// a leak of the build machine layout.
const WORKSPACE_PATH_MARKERS = ['/home/runner/', '\\runneradmin\\', '/github/workspace/']

export function parseArgs(argv) {
  const args = { _: [] }
  for (let index = 0; index < argv.length; index += 1) {
    const token = argv[index]
    if (token.startsWith('--')) {
      const key = token.slice(2)
      const next = argv[index + 1]
      if (!next || next.startsWith('--')) args[key] = true
      else { args[key] = next; index += 1 }
    } else args._.push(token)
  }
  return args
}

class SmokeFailure extends Error {}

function fail(message, details = []) {
  for (const detail of details) console.error(`error: ${detail}`)
  console.error(`error: ${message}`)
  throw new SmokeFailure(message)
}

function sha256File(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex')
}

function listFiles(root, current = root, result = []) {
  for (const entry of readdirSync(current, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
    const full = join(current, entry.name)
    if (entry.isDirectory()) listFiles(root, full, result)
    else if (entry.isFile()) result.push(full.slice(root.length + 1).split(sep).join('/'))
  }
  return result
}

function readReleaseEnv(file) {
  const env = {}
  if (!existsSync(file)) return env
  for (const line of readFileSync(file, 'utf8').split('\n')) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) continue
    const at = trimmed.indexOf('=')
    if (at <= 0) continue
    env[trimmed.slice(0, at).trim()] = trimmed.slice(at + 1).trim()
  }
  return env
}

/** `v24.22.0` -> `24.22`; the artifact is only portable within one minor line. */
function nodeLine(version) {
  const parts = String(version || '').replace(/^v/, '').split('.')
  return parts.length >= 2 ? `${parts[0]}.${parts[1]}` : ''
}

export function verifyChecksums(dir) {
  const sumsFile = join(dir, 'SHA256SUMS')
  if (!existsSync(sumsFile)) return ['SHA256SUMS is missing from the package']
  const expected = new Map()
  for (const line of readFileSync(sumsFile, 'utf8').split('\n')) {
    const trimmed = line.trim()
    if (!trimmed) continue
    const match = /^([0-9a-f]{64})\s{2}(.+)$/.exec(trimmed)
    if (!match) return [`SHA256SUMS has an unparsable line: ${trimmed.slice(0, 120)}`]
    expected.set(match[2], match[1])
  }
  const errors = []
  const present = new Set(listFiles(dir).filter((path) => path !== 'SHA256SUMS'))
  for (const [path, digest] of expected) {
    if (!present.has(path)) { errors.push(`listed file is missing: ${path}`); continue }
    if (sha256File(join(dir, ...path.split('/'))) !== digest) errors.push(`sha256 mismatch: ${path}`)
  }
  for (const path of present) {
    if (!expected.has(path)) errors.push(`unlisted file in the package: ${path}`)
  }
  return errors
}

async function get(url, timeoutMs) {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  try {
    return await fetch(url, { signal: controller.signal, redirect: 'manual', cache: 'no-store' })
  } finally {
    clearTimeout(timer)
  }
}

/**
 * Follow same-origin redirects before judging a page. An unauthenticated root
 * legitimately answers 302 to /login in production; the redirect itself is not a
 * pass, only the 200 render behind it is. Cross-origin hops are refused because
 * the smoke must stay on the package it just booted.
 */
export async function fetchRenderedPage(base, path = '/', { maxRedirects = MAX_REDIRECTS, timeoutMs = 20000 } = {}) {
  const origin = new URL(base).origin
  let url = new URL(path, base)
  let page
  let redirects = 0
  for (;;) {
    page = await get(url, timeoutMs)
    if (!REDIRECT_STATUS.has(page.status)) break
    const location = page.headers.get('location')
    await page.arrayBuffer().catch(() => Buffer.alloc(0))
    if (!location) throw new Error(`redirect from ${url.pathname} has no Location header`)
    const next = new URL(location, url)
    if (next.origin !== origin) throw new Error(`refusing cross-origin redirect ${url.pathname} -> ${next.origin}`)
    redirects += 1
    if (redirects > maxRedirects) throw new Error(`more than ${maxRedirects} redirects starting at ${path}`)
    url = next
  }
  const contentType = page.headers.get('content-type') || ''
  const body = await page.text()
  return { status: page.status, contentType, body, url, redirects }
}

/** Nuxt asset URLs referenced by the rendered HTML, so the archive can prove it shipped them. */
export function collectNuxtAssets(html, limit = 3) {
  const assets = []
  const pattern = /(?:src|href)="(\/_nuxt\/[^"]+)"/g
  let match
  while (assets.length < limit && (match = pattern.exec(html)) !== null) {
    if (!assets.includes(match[1])) assets.push(match[1])
  }
  return assets
}

/** Text files that still name the CI workspace instead of a portable path. */
export function scanWorkspacePaths(root, { maxBytes = 8 * 1024 * 1024 } = {}) {
  const hits = []
  for (const rel of listFiles(root)) {
    const full = join(root, ...rel.split('/'))
    let size = 0
    try { size = statSync(full).size } catch { continue }
    if (size === 0 || size > maxBytes) continue
    const buffer = readFileSync(full)
    if (buffer.includes(0)) continue
    const text = buffer.toString('utf8')
    for (const marker of WORKSPACE_PATH_MARKERS) {
      const at = text.indexOf(marker)
      if (at < 0) continue
      hits.push({ path: rel, marker, sample: text.slice(Math.max(0, at - 40), at + 80).replace(/\s+/g, ' ') })
      break
    }
  }
  return hits
}

function freePort() {
  return new Promise((resolvePort, rejectPort) => {
    const probe = createServer()
    probe.once('error', rejectPort)
    probe.listen(0, '127.0.0.1', () => {
      const { port } = probe.address()
      probe.close(() => resolvePort(port))
    })
  })
}

const delay = (ms) => new Promise((done) => {
  // An unref'd timer must not keep the process alive when the race already settled.
  const timer = setTimeout(done, ms)
  timer.unref?.()
})

async function stopChild(child) {
  if (child.exitCode !== null || child.signalCode !== null) return
  child.kill('SIGTERM')
  const exited = await Promise.race([once(child, 'exit').then(() => true), delay(5000).then(() => false)])
  if (exited) return
  try { child.kill('SIGKILL') } catch { /* already gone */ }
  await Promise.race([once(child, 'exit'), delay(2000)])
}

async function main() {
  const args = parseArgs(process.argv.slice(2))
  if (!args.dir || args.dir === true) fail('usage: smoke-ssr.mjs --dir <extracted ssr dir> [--timeout ms]')
  const dir = resolve(args.dir)
  const timeoutMs = Number(args.timeout) || DEFAULT_TIMEOUT_MS
  const entry = join(dir, '.output', 'server', 'index.mjs')
  if (!existsSync(dir)) fail(`package directory not found: ${dir}`)
  if (!existsSync(entry)) fail(`Nitro entry is missing from the package: ${entry}`)
  const releaseFile = join(dir, 'release.json')
  if (!existsSync(releaseFile)) fail(`release.json is missing from the package: ${releaseFile}`)
  const release = JSON.parse(readFileSync(releaseFile, 'utf8'))

  // 1. No symlinks may survive the archive step.
  const links = scanSymlinks(dir)
  if (links.length > 0) {
    fail(
      `packaged artifact contains ${links.length} symlink(s); unpacked copies cannot resolve build-workspace paths`,
      links.slice(0, MAX_REPORTED_LINKS).map((link) => `${link.kind}: ${link.path} -> ${link.target}`),
    )
  }

  // 2. Same Node line as the build that produced the bundle.
  const expectedNode = nodeLine(release.frontend?.node)
  const currentNode = nodeLine(process.versions.node)
  if (!expectedNode) fail('release.json does not record the Node version used to build the SSR bundle')
  if (expectedNode !== currentNode) {
    fail(
      `release was built on Node ${release.frontend.node} but this smoke test runs ${process.version}`,
      [`use Node ${expectedNode}.x for the packaged-artifact smoke test`],
    )
  }

  // 3. The unpacked bytes still match the checksums written before archiving.
  const checksumErrors = verifyChecksums(dir)
  if (checksumErrors.length > 0) fail('packaged checksums do not match the extracted tree', checksumErrors.slice(0, 20))

  // 4. No file may still name the throwaway CI workspace.
  const workspacePaths = scanWorkspacePaths(dir)
  if (workspacePaths.length > 0) {
    fail(
      `packaged artifact references ${workspacePaths.length} build-workspace path(s)`,
      workspacePaths.slice(0, 10).map((hit) => `${hit.path}: ${hit.marker} ... ${hit.sample}`),
    )
  }

  // 5. Boot the extracted copy exactly the way an operator would.
  const port = await freePort()
  const releaseEnv = readReleaseEnv(join(dir, 'release.env'))
  const child = spawn(process.execPath, [entry], {
    cwd: dir,
    stdio: ['ignore', 'pipe', 'pipe'],
    env: {
      ...process.env,
      ...releaseEnv,
      NODE_ENV: 'production',
      HOST: '127.0.0.1',
      PORT: String(port),
      NITRO_PORT: String(port),
      NITRO_HOST: '127.0.0.1',
      NUXT_SESSION_PASSWORD: releaseEnv.NUXT_SESSION_PASSWORD || 'smoke-test-session-password-0000000000000000',
    },
  })
  const output = []
  const collect = (chunk) => {
    output.push(chunk.toString())
    if (output.length > 200) output.shift()
  }
  child.stdout.on('data', collect)
  child.stderr.on('data', collect)

  const base = `http://127.0.0.1:${port}`
  const deadline = Date.now() + timeoutMs
  let live = null
  let lastError = ''
  try {
    while (Date.now() < deadline) {
      if (child.exitCode !== null || child.signalCode !== null) {
        lastError = `server exited early with code ${child.exitCode} signal ${child.signalCode}`
        break
      }
      try {
        const res = await get(`${base}/health/frontend?live=1`, 5000)
        if (res.status === 200) { live = await res.json(); break }
        lastError = `GET /health/frontend?live=1 -> ${res.status}`
      } catch (error) {
        lastError = `GET /health/frontend?live=1 failed: ${error?.name === 'AbortError' ? 'timeout' : String(error?.message || error)}`
      }
      await delay(500)
    }

    if (!live) {
      fail(
        `SSR server did not become healthy within ${timeoutMs}ms from the unpacked package`,
        [lastError, '--- server output ---', ...output.slice(-40)].filter(Boolean),
      )
    }
    if (live.status !== 'ok' || live.renderer !== 'nuxt-ssr') {
      fail('liveness payload is not a healthy Nuxt SSR renderer', [JSON.stringify(live)])
    }

    // A real page render, not just liveness: the root may legitimately 302 to
    // /login, but a bare 302 is never a pass - the destination must render 200.
    let page
    try {
      page = await fetchRenderedPage(base, '/', { maxRedirects: MAX_REDIRECTS, timeoutMs: 20000 })
    } catch (error) {
      fail(`GET / did not reach a rendered page: ${error?.message || error}`, [...output.slice(-40)])
    }
    if (page.status !== 200) {
      fail(`GET ${page.url.pathname} -> ${page.status} (expected rendered HTML)`, [...output.slice(-40)])
    }
    if (!page.contentType.includes('text/html')) {
      fail(`GET ${page.url.pathname} returned content-type ${page.contentType || '<none>'} instead of text/html`)
    }
    if (!page.body.includes('<div')) fail(`GET ${page.url.pathname} returned no rendered markup`)

    // 6. HTML alone can render while every client asset is missing from the archive.
    const assets = collectNuxtAssets(page.body)
    if (assets.length === 0) console.log('note: rendered page references no /_nuxt/ asset')
    for (const asset of assets) {
      const assetRes = await get(new URL(asset, base), 20000)
      await assetRes.arrayBuffer().catch(() => Buffer.alloc(0))
      if (assetRes.status !== 200) fail(`packaged client asset ${asset} -> ${assetRes.status}`)
    }
    console.log(`page ok: ${page.url.pathname} 200 after ${page.redirects} redirect(s), ${assets.length} client asset(s) verified`)
  } finally {
    await stopChild(child)
  }

  console.log(
    `smoke ok: ${release.product || 'sub2api'} ${release.version || '?'} ` +
    `node ${release.frontend.node} commit ${String(release.commit || '').slice(0, 12)} ` +
    '(0 symlinks, no workspace paths, checksums match, health + rendered page 200)',
  )
}

// Importing this module (tests) must not run the CLI.
function isMainModule() {
  if (!process.argv[1]) return false
  const self = fileURLToPath(import.meta.url)
  try {
    return realpathSync(resolve(process.argv[1])) === realpathSync(self)
  } catch {
    return resolve(process.argv[1]) === self
  }
}

if (isMainModule()) {
  main().catch((error) => {
    // fail() already printed a readable message; anything else is a crash worth showing.
    if (!(error instanceof SmokeFailure)) console.error(`error: ${error?.stack || error}`)
    process.exit(1)
  })
}