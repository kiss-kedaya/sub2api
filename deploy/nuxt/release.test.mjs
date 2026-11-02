import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { collectNuxtAssets, fetchRenderedPage, scanWorkspacePaths } from './smoke-ssr.mjs'
import { copyTreeDereferenced, scanSymlinks } from './release.mjs'

const HERE = fileURLToPath(new URL('.', import.meta.url))
const WORKFLOW = process.env.SSR_WORKFLOW_PATH
  ? resolve(process.cwd(), process.env.SSR_WORKFLOW_PATH)
  : resolve(HERE, '../../.github/workflows/ssr-release.yml')

function workspace(t) {
  const dir = mkdtempSync(join(tmpdir(), 'ssr-pack-'))
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  return dir
}

/** Symlinks need extra privileges on Windows; skip instead of pretending to pass. */
function tryLink(t, target, path, type) {
  try {
    symlinkSync(target, path, type)
    return true
  } catch (error) {
    t.skip(`this host cannot create symlinks (${error.code}); runs on the Linux CI runner`)
    return false
  }
}

async function serve(t, handler) {
  const server = createServer(handler)
  await new Promise((done) => server.listen(0, '127.0.0.1', done))
  t.after(() => {
    server.closeAllConnections?.()
    server.close()
  })
  return `http://127.0.0.1:${server.address().port}`
}

test('stage copy turns absolute file symlinks into real bytes', (t) => {
  const root = workspace(t)
  const outside = join(root, 'workspace', 'node_modules', '.pnpm', '@vueuse+shared@12.0.0', 'node_modules', '@vueuse', 'shared')
  mkdirSync(outside, { recursive: true })
  writeFileSync(join(outside, 'index.mjs'), 'export const shared = 1\n')

  const source = join(root, '.output')
  const bundle = join(source, 'server', 'node_modules', '@vueuse')
  mkdirSync(bundle, { recursive: true })
  writeFileSync(join(source, 'server', 'index.mjs'), 'export {}\n')
  if (!tryLink(t, outside, join(bundle, 'shared'), 'dir')) return

  assert.equal(scanSymlinks(source).length, 1)

  const dest = join(root, 'dist', '.output')
  const stats = copyTreeDereferenced(source, dest)
  assert.equal(stats.dereferenced, 1)
  assert.deepEqual(scanSymlinks(dest), [])
  assert.equal(readFileSync(join(dest, 'server', 'node_modules', '@vueuse', 'shared', 'index.mjs'), 'utf8'), 'export const shared = 1\n')
})

test('stage copy follows nested symlinks inside a linked package', (t) => {
  const root = workspace(t)
  const packageDir = join(root, 'store', 'pkg')
  mkdirSync(join(packageDir, 'dist'), { recursive: true })
  writeFileSync(join(packageDir, 'dist', 'index.js'), 'module.exports = 1\n')
  const sharedDir = join(root, 'store', 'shared')
  mkdirSync(sharedDir, { recursive: true })
  writeFileSync(join(sharedDir, 'index.js'), 'module.exports = 2\n')
  if (!tryLink(t, sharedDir, join(packageDir, 'dist', 'shared'), 'dir')) return

  const source = join(root, '.output')
  mkdirSync(join(source, 'server'), { recursive: true })
  if (!tryLink(t, packageDir, join(source, 'server', 'pkg'), 'dir')) return

  const dest = join(root, 'dist', '.output')
  copyTreeDereferenced(source, dest)
  assert.deepEqual(scanSymlinks(dest), [])
  assert.equal(existsSync(join(dest, 'server', 'pkg', 'dist', 'shared', 'index.js')), true)
})

test('stage copy refuses a broken symlink instead of archiving it', (t) => {
  const root = workspace(t)
  const source = join(root, '.output')
  mkdirSync(join(source, 'server'), { recursive: true })
  if (!tryLink(t, join(root, 'gone', 'missing.mjs'), join(source, 'server', 'missing.mjs'), 'file')) return

  assert.throws(
    () => copyTreeDereferenced(source, join(root, 'dist', '.output')),
    /broken symlink while staging/,
  )
})

test('scanSymlinks reports the runner-workspace links that broke 2.3.0', (t) => {
  const root = workspace(t)
  mkdirSync(join(root, 'server', 'node_modules', '@vueuse'), { recursive: true })
  mkdirSync(join(root, 'server', 'node_modules', '@tanstack'), { recursive: true })
  mkdirSync(join(root, 'elsewhere'), { recursive: true })
  writeFileSync(join(root, 'elsewhere', 'index.mjs'), 'export const inside = 1\n')

  if (!tryLink(t, '/home/runner/work/sub2api/sub2api/frontend/node_modules/.pnpm/x/node_modules/@vueuse/shared', join(root, 'server', 'node_modules', '@vueuse', 'shared'), 'dir')) return
  if (!tryLink(t, join(root, 'elsewhere', 'index.mjs'), join(root, 'server', 'node_modules', 'absolute.mjs'), 'file')) return
  if (!tryLink(t, '../../elsewhere/index.mjs', join(root, 'server', 'node_modules', 'relative.mjs'), 'file')) return

  const links = scanSymlinks(root)
  const broken = links.find((link) => link.path.endsWith('@vueuse/shared'))
  const absolute = links.find((link) => link.path.endsWith('absolute.mjs'))
  const relative = links.find((link) => link.path.endsWith('relative.mjs'))
  assert.equal(broken?.kind, 'broken')
  assert.match(broken.target, /^\/home\/runner\/work\//)
  assert.equal(absolute?.kind, 'absolute')
  assert.equal(relative?.kind, 'relative')
  assert.equal(readFileSync(join(root, 'server', 'node_modules', 'relative.mjs'), 'utf8'), 'export const inside = 1\n')
})

test('ssr-release workflow packages with the immutable workflow commit, smokes the tarball, clobbers the asset', () => {
  assert.equal(existsSync(WORKFLOW), true, `workflow not found at ${WORKFLOW}`)
  const yaml = readFileSync(WORKFLOW, 'utf8')

  assert.match(yaml, /ref: \$\{\{ steps\.ref\.outputs\.tag \}\}/)
  // Packaging tooling must come from the commit that owns this workflow file,
  // never from a moving branch: a tag checkout carries the old, buggy helper.
  assert.match(yaml, /ref: \$\{\{ github\.workflow_sha \}\}/)
  assert.match(yaml, /path: \.pack-tools/)
  assert.doesNotMatch(yaml, /ref: main\b/)
  assert.match(yaml, /cp \.pack-tools\/deploy\/nuxt\/release\.mjs deploy\/nuxt\/release\.mjs/)
  assert.match(yaml, /cp \.pack-tools\/deploy\/nuxt\/smoke-ssr\.mjs deploy\/nuxt\/smoke-ssr\.mjs/)
  assert.match(yaml, /grep -q 'copyTreeDereferenced' deploy\/nuxt\/release\.mjs/)
  assert.match(yaml, /grep -q 'scanWorkspacePaths' deploy\/nuxt\/smoke-ssr\.mjs/)
  assert.match(yaml, /test "\$\{TOOLING_SHA\}" = "\$\{WORKFLOW_SHA\}"/)

  const manifest = yaml.indexOf('Verify the SSR manifest')
  const overlay = yaml.indexOf('Checkout packaging tooling from the workflow commit')
  const seal = yaml.indexOf('Seal the release contract')
  const stage = yaml.indexOf('Stage and package')
  const smoke = yaml.indexOf('Smoke test the packaged tarball')
  const upload = yaml.indexOf('Upload to the GitHub release')
  assert.ok(overlay > manifest, 'tooling overlay must run after the bundle and its manifest exist')
  assert.ok(seal > overlay, 'the sealed contract must be produced by the pinned tooling')
  assert.ok(stage > seal && smoke > stage && upload > smoke, 'stage -> smoke -> upload order')

  // Smoke runs on the bytes that get uploaded, not on the build tree.
  assert.match(yaml, /EXTRACT="\$\(mktemp -d\)"/)
  assert.match(yaml, /tar -xzf/)
  assert.match(yaml, /smoke-ssr\.mjs --dir "\$EXTRACT\/ssr"/)
  assert.match(yaml, /tar -tvzf[\s\S]*?\^l/, 'the tarball must be rejected when it contains symlink entries')
  assert.match(yaml, /gh release upload[\s\S]*?--clobber/)
})

test('smoke follows a same-origin login redirect and never accepts the 302 itself', async (t) => {
  const base = await serve(t, (req, res) => {
    if (req.url === '/') {
      res.writeHead(302, { location: '/login' })
      res.end()
      return
    }
    if (req.url === '/login') {
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' })
      res.end('<!doctype html><html><body><div id="app"></div><script src="/_nuxt/entry.js"></script></body></html>')
      return
    }
    res.writeHead(404)
    res.end()
  })

  const page = await fetchRenderedPage(base, '/')
  assert.equal(page.status, 200)
  assert.equal(page.url.pathname, '/login')
  assert.equal(page.redirects, 1)
  assert.match(page.body, /<div id="app">/)
  assert.deepEqual(collectNuxtAssets(page.body), ['/_nuxt/entry.js'])
})

test('smoke rejects a redirect that leaves the packaged server', async (t) => {
  const base = await serve(t, (req, res) => {
    res.writeHead(302, { location: 'http://127.0.0.1:9/login' })
    res.end()
  })

  await assert.rejects(() => fetchRenderedPage(base, '/'), /refusing cross-origin redirect/)
})

test('smoke rejects a redirect loop instead of passing a 302', async (t) => {
  const base = await serve(t, (req, res) => {
    res.writeHead(302, { location: '/' })
    res.end()
  })

  await assert.rejects(() => fetchRenderedPage(base, '/'), /more than 5 redirects/)
})

test('smoke flags build-workspace paths left inside the archive', (t) => {
  const root = workspace(t)
  mkdirSync(join(root, '.output', 'server'), { recursive: true })
  writeFileSync(join(root, '.output', 'server', 'index.mjs'), 'const from = "/home/runner/work/sub2api/sub2api/frontend/.output"\n')
  writeFileSync(join(root, '.output', 'server', 'clean.mjs'), 'const from = "./chunks"\n')
  writeFileSync(join(root, '.output', 'server', 'blob.bin'), Buffer.from([0, 47, 104, 111, 109, 101, 0]))

  const hits = scanWorkspacePaths(root)
  assert.equal(hits.length, 1)
  assert.equal(hits[0].path, '.output/server/index.mjs')
  assert.match(hits[0].sample, /\/home\/runner\/work\//)
})