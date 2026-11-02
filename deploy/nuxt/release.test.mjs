import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
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
  mkdirSync(join(root, 'elsewhere'), { recursive: true })
  writeFileSync(join(root, 'elsewhere', 'index.mjs'), '\n')
  mkdirSync(join(root, 'server', 'node_modules', '@tanstack'), { recursive: true })

  if (!tryLink(t, '/home/runner/work/sub2api/sub2api/frontend/node_modules/.pnpm/x/node_modules/@vueuse/shared', join(root, 'server', 'node_modules', '@vueuse', 'shared'), 'dir')) return
  if (!tryLink(t, join(root, 'elsewhere', 'index.mjs'), join(root, 'server', 'node_modules', 'inside.mjs'), 'file')) return

  const links = scanSymlinks(root)
  const absolute = links.find((link) => link.path.endsWith('@vueuse/shared'))
  const inside = links.find((link) => link.path.endsWith('inside.mjs'))
  assert.equal(absolute?.kind, 'broken')
  assert.match(absolute.target, /^\/home\/runner\/work\//)
  assert.equal(inside?.kind, 'absolute')
  assert.equal(readFileSync(join(root, inside.path), 'utf8'), '\n')
})

test('ssr-release workflow rebuilds from main tooling, smokes the tarball, clobbers the asset', () => {
  assert.equal(existsSync(WORKFLOW), true, `workflow not found at ${WORKFLOW}`)
  const yaml = readFileSync(WORKFLOW, 'utf8')

  assert.match(yaml, /ref: \$\{\{ steps\.ref\.outputs\.tag \}\}/)
  // Packaging tooling must come from main: a tag checkout carries the old, buggy helper.
  assert.match(yaml, /uses: actions\/checkout@v[0-9]+[\s\S]*?ref: main[\s\S]*?path: \.pack-tools/)
  assert.match(yaml, /cp \.pack-tools\/deploy\/nuxt\/release\.mjs deploy\/nuxt\/release\.mjs/)
  assert.match(yaml, /cp \.pack-tools\/deploy\/nuxt\/smoke-ssr\.mjs deploy\/nuxt\/smoke-ssr\.mjs/)
  assert.match(yaml, /grep -q 'copyTreeDereferenced' deploy\/nuxt\/release\.mjs/)

  const overlay = yaml.indexOf('Checkout packaging tooling from main')
  const seal = yaml.indexOf('Seal the release contract')
  const stage = yaml.indexOf('Stage and package')
  const smoke = yaml.indexOf('Smoke test the packaged tarball')
  const upload = yaml.indexOf('Upload to the GitHub release')
  assert.ok(overlay > seal, 'tooling overlay must run after the build-time seal so the manifest stays clean')
  assert.ok(stage > overlay, 'staging must use the overlaid helper')
  assert.ok(smoke > stage && smoke < upload, 'the tarball must be smoked after packaging and before upload')

  // Smoke runs on the bytes that get uploaded, not on the build tree.
  assert.match(yaml, /tar -xzf/)
  assert.match(yaml, /deploy\/nuxt\/smoke-ssr\.mjs --dir/)
  assert.match(yaml, /tar -tvzf[\s\S]*?\^l/, 'the tarball must be rejected when it contains symlink entries')
  assert.match(yaml, /gh release upload[\s\S]*?--clobber/)
})
