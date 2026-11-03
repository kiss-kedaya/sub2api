import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { execFileSync } from 'node:child_process'

const script = new URL('./verify-spa-bundle.mjs', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')

/** Build a throwaway frontend + dist pair and run the verifier on it. */
function fixture({ html, files = {}, nuxtConfig = false, noEmbed = false }) {
  const root = mkdtempSync(join(tmpdir(), 'spa-verify-'))
  const frontend = join(root, 'frontend')
  const webDir = join(root, 'web')
  const dist = join(webDir, 'dist')
  mkdirSync(frontend, { recursive: true })
  mkdirSync(join(dist, 'assets'), { recursive: true })
  writeFileSync(join(frontend, 'package.json'), '{}')
  if (nuxtConfig) writeFileSync(join(frontend, 'nuxt.config.ts'), 'export default {}')
  if (!noEmbed) writeFileSync(join(webDir, 'embed_on.go'), '//go:embed all:dist\npackage web\n')
  writeFileSync(join(dist, 'index.html'), html)
  writeFileSync(join(dist, 'assets', 'index-abc123.js'), 'console.log(1)')
  writeFileSync(join(dist, 'assets', 'index-abc123.css'), 'body{}')
  for (const [name, body] of Object.entries(files)) {
    mkdirSync(join(dist, name, '..'), { recursive: true })
    writeFileSync(join(dist, name), body)
  }
  return { root, frontend, dist, cleanup: () => rmSync(root, { recursive: true, force: true }) }
}

function run(frontend, dist) {
  try {
    const out = execFileSync(process.execPath, [script, frontend, dist], { encoding: 'utf8' })
    return { code: 0, out }
  } catch (e) {
    return { code: e.status ?? 1, out: `${e.stdout ?? ''}${e.stderr ?? ''}` }
  }
}

const GOOD_HTML = `<!doctype html><html><head>
<link rel="stylesheet" href="/assets/index-abc123.css">
<script type="module" src="/assets/index-abc123.js"></script>
</head><body><div id="app"></div></body></html>`

test('accepts a well-formed SPA bundle', () => {
  const f = fixture({ html: GOOD_HTML })
  try {
    const r = run(f.frontend, f.dist)
    assert.equal(r.code, 0, r.out)
    assert.match(r.out, /contract OK/)
  } finally { f.cleanup() }
})

test('rejects a bundle with no index.html', () => {
  const f = fixture({ html: GOOD_HTML })
  try {
    rmSync(join(f.dist, 'index.html'))
    const r = run(f.frontend, f.dist)
    assert.equal(r.code, 1)
    assert.match(r.out, /missing index.html/)
  } finally { f.cleanup() }
})

test('rejects an index.html that references a missing asset', () => {
  // This is the white-screen case: the page loads and the script 404s.
  const f = fixture({
    html: `<!doctype html><html><head>
<script type="module" src="/assets/index-GONE.js"></script>
</head><body></body></html>`,
  })
  try {
    const r = run(f.frontend, f.dist)
    assert.equal(r.code, 1)
    assert.match(r.out, /missing file: assets\/index-GONE\.js/)
  } finally { f.cleanup() }
})

test('rejects retired SSR output left in the bundle', () => {
  const f = fixture({ html: GOOD_HTML, files: { '.output/server/index.mjs': 'x' } })
  try {
    const r = run(f.frontend, f.dist)
    assert.equal(r.code, 1)
    assert.match(r.out, /retired SSR output/)
  } finally { f.cleanup() }
})

test('rejects a nuxt.config.ts left in the frontend', () => {
  const f = fixture({ html: GOOD_HTML, nuxtConfig: true })
  try {
    const r = run(f.frontend, f.dist)
    assert.equal(r.code, 1)
    assert.match(r.out, /nuxt\.config\.ts exists/)
  } finally { f.cleanup() }
})

test('rejects a bundle the Go binary would not embed', () => {
  const f = fixture({ html: GOOD_HTML, noEmbed: true })
  try {
    const r = run(f.frontend, f.dist)
    assert.equal(r.code, 1)
    assert.match(r.out, /embed_on\.go not found/)
  } finally { f.cleanup() }
})

test('rejects absolute build-machine paths', () => {
  const f = fixture({
    html: `<!doctype html><html><head>
<script type="module" src="/home/runner/work/sub2api/frontend/dist/assets/index-abc123.js"></script>
</head><body></body></html>`,
  })
  try {
    const r = run(f.frontend, f.dist)
    assert.equal(r.code, 1)
    assert.match(r.out, /absolute build-machine path/)
  } finally { f.cleanup() }
})
