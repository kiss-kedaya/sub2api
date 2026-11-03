#!/usr/bin/env node
/**
 * Assert the SPA deploy contract for the static bundle we ship.
 *
 * The frontend is a Vite SPA embedded into the Go binary. That only works when
 * the bundle and the server agree on three things, and getting any of them
 * wrong produces a page that loads and then fails at runtime:
 *
 *   1. `index.html` exists and references hashed assets under a relative or
 *      root-relative base, never an absolute filesystem path.
 *   2. Every asset `index.html` references is actually present in the bundle.
 *      A missing chunk is a white screen, not a 404 the user can see.
 *   3. No SSR/Nitro output leaked into the artifact. If `.output/` or a Nitro
 *      server entry is present, someone is still building the retired stack.
 *
 * Usage: node deploy/spa/verify-spa-bundle.mjs <frontend-dir> <dist-dir>
 */
import { readFileSync, existsSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, posix } from 'node:path'

const [frontendDir, distDir] = process.argv.slice(2)
if (!frontendDir || !distDir) {
  console.error('usage: verify-spa-bundle.mjs <frontend-dir> <dist-dir>')
  process.exit(2)
}

const problems = []
const fail = (msg) => problems.push(msg)

/** Collect every file under dir, relative to it. */
function walk(dir, base = dir, out = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) walk(full, base, out)
    else if (entry.isFile()) out.push(full.slice(base.length + 1).split('\\').join('/'))
  }
  return out
}

// --- 1. bundle exists ------------------------------------------------------
if (!existsSync(distDir) || !statSync(distDir).isDirectory()) {
  console.error(`FAIL: dist directory missing: ${distDir}`)
  console.error('      run `pnpm run build` in frontend/ first')
  process.exit(1)
}
const files = walk(distDir)
const hasIndex = files.includes('index.html')
if (!hasIndex) fail(`missing index.html in ${distDir}`)
if (!files.some((f) => f.startsWith('assets/'))) {
  fail('missing assets/ directory: Vite output is not present')
}

// --- 2. SSR must not be shipped -------------------------------------------
for (const stale of ['.output', '.nitro', 'server/index.mjs']) {
  if (files.some((f) => f === stale || f.startsWith(stale + '/'))) {
    fail(`retired SSR output found in the bundle: ${stale}`)
  }
}
if (existsSync(join(frontendDir, 'nuxt.config.ts'))) {
  fail('frontend/nuxt.config.ts exists: the SPA is the only supported frontend')
}

// --- 3. index.html references resolve -------------------------------------
if (hasIndex) {
  const html = readFileSync(join(distDir, 'index.html'), 'utf8')

  // Absolute filesystem paths or a build-time machine path would 404 in prod.
  if (/["'(]\/(?:home|Users|builds|github|runner)\//.test(html)) {
    fail('index.html references an absolute build-machine path')
  }
  if (/["'(]file:\/\//.test(html)) {
    fail('index.html references a file:// URL')
  }

  const refs = new Set()
  for (const m of html.matchAll(/(?:src|href)="([^"]+)"/g)) {
    const raw = m[1]
    if (!raw || raw.startsWith('data:') || raw.startsWith('http') || raw.startsWith('//')) continue
    if (raw.startsWith('#')) continue
    // Root-relative is served from the SPA root; relative resolves next to it.
    const rel = raw.startsWith('/') ? raw.slice(1) : raw
    if (rel) refs.add(posix.normalize(rel))
  }
  for (const ref of refs) {
    if (ref.startsWith('api/') || ref === 'favicon.ico') continue
    if (!files.includes(ref)) fail(`index.html references a missing file: ${ref}`)
  }
  if (refs.size === 0) fail('index.html references no assets at all')
}

// --- 4. the SPA entry must be the Go embed source -------------------------
// backend/internal/web embeds this exact directory; a mismatch means the
// binary would serve a different bundle than the one we just verified.
const embedGo = join(dirname(distDir), 'embed_on.go')
if (existsSync(embedGo)) {
  const src = readFileSync(embedGo, 'utf8')
  if (!src.includes('//go:embed all:dist')) {
    fail('backend/internal/web/embed_on.go no longer embeds all:dist')
  }
} else {
  fail(`embed_on.go not found next to ${distDir}`)
}

if (problems.length) {
  console.error('SPA bundle contract FAILED:')
  for (const p of problems) console.error('  - ' + p)
  process.exit(1)
}
console.log(`SPA bundle contract OK (${files.length} files, index.html + assets present)`)
