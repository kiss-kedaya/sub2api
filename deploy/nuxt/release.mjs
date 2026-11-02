#!/usr/bin/env node
/**
 * Combined SSR + Go release manifest.
 *
 * A release is two artifacts that must agree on one commit: the Go binary
 * (with the legacy Vite SPA embedded as the rollback UI) and the Nuxt/Nitro
 * `.output` directory. `seal` writes the contract, `verify` re-checks it before
 * deployment, and `stage` lays out the deployable directory.
 *
 *   node deploy/nuxt/release.mjs seal   --ssr frontend/.output --binary backend/bin/server --out release.json
 *   node deploy/nuxt/release.mjs verify --release release.json --ssr frontend/.output --binary backend/bin/server
 *   node deploy/nuxt/release.mjs stage  --release release.json --ssr frontend/.output --binary backend/bin/server --out dist/ssr
 *
 * Run from the repository root (it reuses frontend/scripts/build-manifest.mjs).
 */
import { createHash } from 'node:crypto'
import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, readdirSync, readlinkSync, realpathSync, statSync, writeFileSync } from 'node:fs'
import { basename, dirname, join, resolve, sep } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = resolve(HERE, '../..')
const HELPER = join(REPO_ROOT, 'frontend/scripts/build-manifest.mjs')

if (!existsSync(HELPER)) {
  console.error('error: frontend/scripts/build-manifest.mjs is required; run this from a full checkout')
  process.exit(1)
}
const { verifyManifest, MANIFEST_NAME } = await import(pathToFileURL(HELPER).href)

function sha256File(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex')
}

function listFiles(root, current = root, result = []) {
  for (const entry of readdirSync(current, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
    const full = join(current, entry.name)
    if (entry.isDirectory()) listFiles(root, full, result)
    else if (entry.isFile()) result.push(full)
  }
  return result
}

const MAX_COPY_ENTRIES = 400000
const MAX_COPY_DEPTH = 48

function isAbsoluteLinkTarget(target) {
  return target.startsWith('/') || /^[A-Za-z]:[\\/]/.test(target) || target.startsWith('\\\\')
}

/**
 * Copy a tree while materialising every symlink into plain files.
 *
 * Nitro's node-file-trace links external packages (for example
 * `@vueuse/shared` and `@tanstack/virtual-core`) to absolute build-workspace
 * paths such as `/home/runner/work/<repo>/node_modules/.pnpm/...`. Archiving the
 * tree as-is ships those links to every server, where they are dead and the
 * Nitro server exits with ERR_MODULE_NOT_FOUND on the first page render, so the
 * staged tree must contain regular files only.
 */
export function copyTreeDereferenced(srcRoot, destRoot) {
  const sourceRoot = resolve(srcRoot)
  const targetRoot = resolve(destRoot)
  const stats = { files: 0, dirs: 0, dereferenced: 0 }
  const stack = []

  const copyDirectory = (from, to, depth) => {
    if (depth > MAX_COPY_DEPTH) throw new Error(`copy exceeded ${MAX_COPY_DEPTH} levels below ${sourceRoot} (symlink loop?)`)
    const real = realpathSync(from)
    if (stack.includes(real)) throw new Error(`symlink cycle: ${from} -> ${real}`)
    stack.push(real)
    mkdirSync(to, { recursive: true })
    stats.dirs += 1
    for (const entry of readdirSync(from, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      if (stats.files + stats.dirs > MAX_COPY_ENTRIES) throw new Error(`copy exceeded ${MAX_COPY_ENTRIES} entries below ${sourceRoot}`)
      const link = join(from, entry.name)
      const next = join(to, entry.name)
      if (entry.isSymbolicLink()) {
        const raw = readlinkSync(link)
        let real
        try {
          real = realpathSync(link)
        } catch {
          throw new Error(`broken symlink while staging: ${link} -> ${raw}`)
        }
        stats.dereferenced += 1
        const info = statSync(real)
        if (info.isDirectory()) copyDirectory(real, next, depth + 1)
        else {
          copyFileSync(real, next)
          try { chmodSync(next, info.mode) } catch { /* best effort on non-POSIX filesystems */ }
          stats.files += 1
        }
        continue
      }
      if (entry.isDirectory()) copyDirectory(link, next, depth + 1)
      else if (entry.isFile()) {
        copyFileSync(link, next)
        try { chmodSync(next, statSync(link).mode) } catch { /* best effort */ }
        stats.files += 1
      }
    }
    stack.pop()
  }

  copyDirectory(sourceRoot, targetRoot, 0)
  return stats
}

/**
 * List every symlink below `root` and classify it. An archived release must
 * return an empty list: `absolute`, `outside-tree` and `broken` entries all
 * point at the throwaway CI checkout instead of real bytes.
 */
export function scanSymlinks(root) {
  const rootDir = resolve(root)
  let realRoot = rootDir
  try { realRoot = realpathSync(rootDir) } catch { /* keep the literal path */ }
  const found = []

  const walk = (dir) => {
    let entries
    try { entries = readdirSync(dir, { withFileTypes: true }) } catch { return }
    for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
      const full = join(dir, entry.name)
      if (entry.isSymbolicLink()) {
        let target = ''
        try { target = readlinkSync(full) } catch { target = '<unreadable>' }
        let kind = isAbsoluteLinkTarget(target) ? 'absolute' : 'relative'
        let resolved = null
        try { resolved = realpathSync(full) } catch { kind = 'broken' }
        if (resolved && kind !== 'broken' && resolved !== realRoot && !resolved.startsWith(realRoot + sep)) {
          kind = 'outside-tree'
        }
        found.push({ path: full.slice(rootDir.length + 1).split(sep).join('/'), target, kind })
        continue
      }
      if (entry.isDirectory()) walk(full)
    }
  }

  walk(rootDir)
  return found
}

function assertNoSymlinks(root, label) {
  const links = scanSymlinks(root)
  if (links.length === 0) return
  fail(
    `${label} still contains ${links.length} symlink(s); the archive would ship build-workspace paths`,
    links.slice(0, 20).map((link) => `${link.kind}: ${link.path} -> ${link.target}`),
  )
}

function resolveSsrManifestDir(ssrDir) {
  const publicDir = join(resolve(ssrDir), 'public')
  if (existsSync(join(publicDir, MANIFEST_NAME))) return publicDir
  return resolve(ssrDir)
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

function fail(message, errors = []) {
  for (const error of errors) console.error(`error: ${error}`)
  console.error(`error: ${message}`)
  process.exit(1)
}

function seal(args) {
  const ssrDir = resolve(args.ssr || join(REPO_ROOT, 'frontend/.output'))
  const binary = resolve(args.binary || join(REPO_ROOT, 'backend/bin/server'))
  const out = resolve(args.out || join(REPO_ROOT, 'release.json'))
  const spaDist = args['spa-dist'] ? resolve(args['spa-dist']) : ''
  if (!spaDist) fail('seal requires --spa-dist so the embedded rollback UI is part of the version contract')

  const manifestDir = resolveSsrManifestDir(ssrDir)
  const ssrCheck = verifyManifest(manifestDir, {
    mode: 'ssr',
    expectVersion: args.version,
    allowCommitDrift: Boolean(args['allow-commit-drift']),
  })
  if (ssrCheck.errors.length > 0) fail('SSR bundle failed verification', ssrCheck.errors)
  if (!existsSync(binary)) fail(`Go binary not found: ${binary}`)

  const ssrManifest = ssrCheck.manifest
  let embeddedFrontend = null
  if (spaDist) {
    const spaCheck = verifyManifest(spaDist, {
      mode: 'spa',
      binary,
      allowCommitDrift: Boolean(args['allow-commit-drift']),
    })
    if (spaCheck.errors.length > 0) fail('embedded SPA fallback does not match the Go binary', spaCheck.errors)
    if (spaCheck.manifest.version !== ssrManifest.version) {
      fail(`SPA version ${spaCheck.manifest.version} != SSR version ${ssrManifest.version}`)
    }
    if (spaCheck.manifest.commit !== ssrManifest.commit) {
      fail(`SPA commit ${spaCheck.manifest.commit} != SSR commit ${ssrManifest.commit}`)
    }
    embeddedFrontend = {
      mode: 'spa',
      manifestHash: spaCheck.manifest.manifestHash,
      version: spaCheck.manifest.version,
      commit: spaCheck.manifest.commit,
    }
  }

  const binaryInfo = statSync(binary)
  const release = {
    schema: 1,
    product: 'sub2api',
    version: ssrManifest.version,
    commit: ssrManifest.commit,
    builtAt: new Date().toISOString(),
    frontend: {
      mode: 'ssr',
      manifestHash: ssrManifest.manifestHash,
      version: ssrManifest.version,
      commit: ssrManifest.commit,
      node: ssrManifest.node,
      builtAt: ssrManifest.builtAt,
      entry: ssrManifest.entry,
    },
    backend: {
      path: basename(binary),
      sha256: sha256File(binary),
      size: binaryInfo.size,
      embeddedFrontend,
    },
  }
  mkdirSync(dirname(out), { recursive: true })
  writeFileSync(out, `${JSON.stringify(release, null, 2)}\n`)
  console.log(
    `release sealed: version=${release.version} commit=${release.commit.slice(0, 12)} ` +
    `ssr=${release.frontend.manifestHash.slice(0, 16)} backend=${release.backend.sha256.slice(0, 16)}`,
  )
}

function verify(args) {
  const file = resolve(args.release || join(REPO_ROOT, 'release.json'))
  if (!existsSync(file)) fail(`release manifest not found: ${file}`)
  const release = JSON.parse(readFileSync(file, 'utf8'))
  const errors = []

  if (release.schema !== 1) errors.push(`schema ${release.schema} != 1`)
  if (release.product !== 'sub2api') errors.push(`product "${release.product}" != "sub2api"`)

  const ssrDir = resolve(args.ssr || join(REPO_ROOT, 'frontend/.output'))
  const manifestDir = resolveSsrManifestDir(ssrDir)
  const ssrCheck = verifyManifest(manifestDir, { mode: 'ssr', allowCommitDrift: Boolean(args['allow-commit-drift']) })
  errors.push(...ssrCheck.errors)
  if (ssrCheck.manifest) {
    if (ssrCheck.manifest.manifestHash !== release.frontend?.manifestHash) {
      errors.push(`SSR manifestHash ${ssrCheck.manifest.manifestHash} != sealed ${release.frontend?.manifestHash}`)
    }
    if (ssrCheck.manifest.commit !== release.commit) {
      errors.push(`SSR commit ${ssrCheck.manifest.commit} != release commit ${release.commit}`)
    }
    if (ssrCheck.manifest.version !== release.version) {
      errors.push(`SSR version ${ssrCheck.manifest.version} != release version ${release.version}`)
    }
  }

  const binary = args.binary
    ? resolve(args.binary)
    : release.backend?.path
      ? resolve(dirname(file), release.backend.path)
      : resolve(join(REPO_ROOT, 'backend/bin/server'))
  if (!existsSync(binary)) {
    errors.push(`Go binary not found: ${binary}`)
  } else {
    const digest = sha256File(binary)
    if (digest !== release.backend?.sha256) errors.push(`binary sha256 ${digest} != sealed ${release.backend?.sha256}`)
    if (statSync(binary).size !== release.backend?.size) errors.push('binary size changed since seal')
    const embedded = release.backend?.embeddedFrontend
    if (!embedded?.manifestHash) errors.push('release does not bind an embedded SPA manifest hash')
    else if (!readFileSync(binary).includes(Buffer.from(embedded.manifestHash))) {
      errors.push(`binary does not embed SPA manifestHash ${embedded.manifestHash}`)
    }
    if (embedded?.commit && !readFileSync(binary).includes(Buffer.from(`\"commit\": \"${embedded.commit}\"`))) {
      errors.push(`binary does not embed SPA commit ${embedded.commit}`)
    }
  }

  if (args['spa-dist']) {
    const spaCheck = verifyManifest(resolve(args['spa-dist']), {
      mode: 'spa',
      binary,
      allowCommitDrift: Boolean(args['allow-commit-drift']),
    })
    errors.push(...spaCheck.errors)
    if (spaCheck.manifest) {
      if (spaCheck.manifest.version !== release.version) errors.push(`SPA version ${spaCheck.manifest.version} != release version ${release.version}`)
      if (spaCheck.manifest.commit !== release.commit) errors.push(`SPA commit ${spaCheck.manifest.commit} != release commit ${release.commit}`)
      if (spaCheck.manifest.manifestHash !== release.backend?.embeddedFrontend?.manifestHash) {
        errors.push(`SPA manifestHash ${spaCheck.manifest.manifestHash} != sealed embedded hash ${release.backend?.embeddedFrontend?.manifestHash}`)
      }
    }
  }

  if (errors.length > 0) fail('release verification failed', errors)
  console.log(
    `release verified: version=${release.version} commit=${release.commit.slice(0, 12)} ` +
    `ssr=${release.frontend.manifestHash.slice(0, 16)} backend=${release.backend.sha256.slice(0, 16)}`,
  )
}

function stage(args) {
  const file = resolve(args.release || join(REPO_ROOT, 'release.json'))
  if (!existsSync(file)) fail(`release manifest not found: ${file}`)
  const release = JSON.parse(readFileSync(file, 'utf8'))
  const ssrDir = resolve(args.ssr || join(REPO_ROOT, 'frontend/.output'))
  const binary = args.binary
    ? resolve(args.binary)
    : release.backend?.path
      ? resolve(dirname(file), release.backend.path)
      : resolve(join(REPO_ROOT, 'backend/bin/server'))
  const out = resolve(args.out || join(REPO_ROOT, 'dist/ssr'))
  if (!existsSync(binary)) fail(`Go binary not found: ${binary}`)
  if (!existsSync(ssrDir)) fail(`SSR output not found: ${ssrDir}`)

  if (existsSync(out)) fail(`staging directory already exists; choose a fresh --out path: ${out}`)
  verify({ ...args, release: file, ssr: ssrDir, binary })
  mkdirSync(out, { recursive: true })
  copyFileSync(binary, join(out, 'sub2api'))
  const copyStats = copyTreeDereferenced(ssrDir, join(out, '.output'))
  console.log(
    `dereferenced ${copyStats.dereferenced} symlink(s) while staging .output ` +
    `(${copyStats.files} files, ${copyStats.dirs} dirs)`,
  )
  copyFileSync(file, join(out, 'release.json'))
  writeFileSync(join(out, 'VERSION'), `${release.version}\n`, 'utf8')
  writeFileSync(join(out, 'release.env'), [
    `NUXT_SSR_VERSION=${release.frontend.version}`,
    `NUXT_SSR_COMMIT=${release.frontend.commit}`,
    `NUXT_SSR_BUILT_AT=${release.frontend.builtAt || ''}`,
    `NUXT_EXPECTED_BACKEND_VERSION=${release.version}`,
  ].join('\n') + '\n', 'utf8')
  // Ship the deploy templates next to the artifacts so an operator does not need the repo.
  const deployTemplates = join(HERE)
  for (const name of ['Dockerfile', 'sub2api-ssr.service', 'nginx-ssr.example.conf', 'ssr.env.example', 'RELEASE.md']) {
    const source = join(deployTemplates, name)
    if (!existsSync(source)) continue
    mkdirSync(join(out, 'deploy'), { recursive: true })
    copyFileSync(source, join(out, 'deploy', name))
  }
  // Dereferencing must not change a single byte: the sealed manifest already
  // hashed the traced closure, so the staged copy has to match it exactly.
  const stagedManifestDir = resolveSsrManifestDir(join(out, '.output'))
  const stagedCheck = verifyManifest(stagedManifestDir, { mode: 'ssr', allowCommitDrift: true })
  if (stagedCheck.errors.length > 0) {
    fail('staged .output no longer matches the sealed manifest after dereferencing', stagedCheck.errors.slice(0, 20))
  }
  assertNoSymlinks(out, 'staged release')
  const sumFiles = listFiles(out).filter((path) => path !== join(out, 'SHA256SUMS'))
  const sums = sumFiles.map((path) => `${sha256File(path)}  ${path.slice(out.length + 1).split('\\').join('/')}`).join('\n') + '\n'
  writeFileSync(join(out, 'SHA256SUMS'), sums)
  console.log(`staged ${out}`)
  console.log(`next: tar -C ${dirname(out)} -czf sub2api_${release.version}_ssr_linux_amd64.tar.gz ${basename(out)}`)
}

function main() {
  const args = parseArgs(process.argv.slice(2))
  const command = args._[0]
  if (!command || args.help) {
    console.log('usage: release.mjs <seal|verify|stage> --ssr <dir> --binary <file> [--spa-dist <dir>] [--release <file>] [--out <path>]')
    console.log('  stage dereferences build-workspace symlinks and refuses to emit a tree that still contains any')
    process.exit(command ? 0 : 2)
  }
  if (command === 'seal') return seal(args)
  if (command === 'verify') return verify(args)
  if (command === 'stage') return stage(args)
  fail(`unknown command "${command}"`)
}

// Importing this module (tests, smoke checks) must not run the CLI.
function isMainModule() {
  if (!process.argv[1]) return false
  const self = fileURLToPath(import.meta.url)
  try {
    return realpathSync(resolve(process.argv[1])) === realpathSync(self)
  } catch {
    return resolve(process.argv[1]) === self
  }
}

if (isMainModule()) main()
