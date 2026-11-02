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
 *   4. `node .output/server/index.mjs` answers `/health/frontend?live=1` and `/`
 *      with 200.
 */
import { spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { createServer } from 'node:net'
import { join, resolve, sep } from 'node:path'
import { once } from 'node:events'
import { scanSymlinks } from './release.mjs'

const DEFAULT_TIMEOUT_MS = 90000
const MAX_REPORTED_LINKS = 20

function parseArgs(argv) {
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

function verifyChecksums(dir) {
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

async function get(url, timeoutMs) {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  try {
    return await fetch(url, { signal: controller.signal, redirect: 'manual', cache: 'no-store' })
  } finally {
    clearTimeout(timer)
  }
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

  // 4. Boot the extracted copy exactly the way an operator would.
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

    // An unauthenticated application may redirect / to /login. Still render the
    // destination: liveness alone cannot detect missing Nitro runtime modules.
    let pageUrl = new URL('/', base)
    let page
    for (let redirects = 0; redirects <= 5; redirects += 1) {
      page = await get(pageUrl, 20000)
      if (![301, 302, 303, 307, 308].includes(page.status)) break
      const location = page.headers.get('location')
      if (!location || redirects === 5) fail('SSR page redirect is missing or loops')
      const next = new URL(location, pageUrl)
      if (next.origin !== base) fail('SSR page redirects outside the local smoke server')
      pageUrl = next
    }
    const contentType = page.headers.get('content-type') || ''
    const body = await page.text()
    if (page.status !== 200) fail(`GET ${pageUrl.pathname} -> ${page.status} (expected rendered HTML)`, [...output.slice(-40)])
    if (!contentType.includes('text/html')) fail(`GET / returned content-type ${contentType || '<none>'} instead of text/html`)
    if (!body.includes('<div')) fail('GET / returned no rendered markup')
  } finally {
    await stopChild(child)
  }

  console.log(
    `smoke ok: ${release.product || 'sub2api'} ${release.version || '?'} ` +
    `node ${release.frontend.node} commit ${String(release.commit || '').slice(0, 12)} ` +
    `(0 symlinks, checksums match, /health/frontend?live=1 and / both 200)`,
  )
}

main().catch((error) => {
  // fail() already printed a readable message; anything else is a crash worth showing.
  if (!(error instanceof SmokeFailure)) console.error(`error: ${error?.stack || error}`)
  process.exit(1)
})
