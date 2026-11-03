import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import { readBackendOrigin, validateBffHaConfig } from './check-bff-ha.mjs'

const TEMPLATE_PATH = fileURLToPath(new URL('./nginx-ssr.example.conf', import.meta.url))
const ENV_PATH = fileURLToPath(new URL('./ssr.env.example', import.meta.url))
const CHECKER_PATH = fileURLToPath(new URL('./check-bff-ha.mjs', import.meta.url))
const TEMPLATE = readFileSync(TEMPLATE_PATH, 'utf8')
const ORIGIN = 'http://127.0.0.1:3187'

function resultOf(text, options = {}) {
  return validateBffHaConfig(text, { backendOrigin: ORIGIN, ...options })
}

function errorsOf(text, options) {
  return resultOf(text, options).errors.join('\n')
}

test('example is a 3187 dispatch HA config with two same-version Go backends', () => {
  const result = resultOf(TEMPLATE)
  assert.equal(result.ok, true, result.errors.join('\n'))
  assert.equal(result.upstream?.servers.length, 2)
  assert.equal(result.dispatchUpstream?.servers.length, 2)
  assert.equal(result.dispatchUpstream?.servers[1].params.backup, true)
  assert.equal(result.dispatchLocations, 1)
  assert.deepEqual(result.dispatchUpstream?.servers.map((server) => server.address), ['127.0.0.1:8080', '192.0.2.10:8080'])
})

test('ssr.env.example points at the local 3187 dispatch, not a Go port', () => {
  assert.equal(readBackendOrigin(readFileSync(ENV_PATH, 'utf8')), ORIGIN)
})

test('rejects a single Go dispatch slot', () => {
  const broken = TEMPLATE.replace(/\n\s*server 192\.0\.2\.10:8080[^;]*;/, '')
  assert.match(errorsOf(broken), /3187 dispatch upstream .*need at least 2/)
})

test('rejects a dispatch without a peer backup', () => {
  const broken = TEMPLATE.replace(
    '192.0.2.10:8080 max_fails=2 fail_timeout=10s backup;',
    '192.0.2.10:8080 max_fails=2 fail_timeout=10s;',
  )
  assert.match(errorsOf(broken), /needs a peer backup/)
})

test('rejects a missing 3187 dispatch server', () => {
  const broken = TEMPLATE.replace('listen 127.0.0.1:3187;', 'listen 127.0.0.1:3188;')
  assert.match(errorsOf(broken), /missing server listen 127\.0\.0\.1:3187/)
})

test('rejects a public dispatch listen', () => {
  const broken = TEMPLATE.replace('listen 127.0.0.1:3187;', 'listen 3187;')
  assert.match(errorsOf(broken), /must bind 127\.0\.0\.1:3187 \(loopback\)/)
})

test('rejects a literal Go backend in the 3187 dispatch', () => {
  const broken = TEMPLATE.replace('proxy_pass http://sub2api_go_bff;', 'proxy_pass http://127.0.0.1:8080;')
  assert.match(errorsOf(broken), /single-backend 502 bug|bypasses upstream/)
})

test('rejects NUXT_BACKEND_ORIGIN pointing at a Go port', () => {
  assert.match(
    errorsOf(TEMPLATE, { backendOrigin: 'http://127.0.0.1:8080' }),
    /must point at http:\/\/127\.0\.0\.1:3187/,
  )
})

test('single outer Node slot is a warning, not the root-cause failure', () => {
  const broken = TEMPLATE.replace(/\n\s*server 127\.0\.0\.1:3119[^;]*;/, '')
  const result = resultOf(broken)
  assert.equal(result.ok, true, result.errors.join('\n'))
  assert.match(result.warnings.join('\n'), /outer Node HA is not the 3187 dispatch root-cause fix/)
})

test('rejects gateway traffic sent to the 3187 dispatch', () => {
  const broken = TEMPLATE.replace('proxy_pass http://127.0.0.1:8080;', 'proxy_pass http://sub2api_go_bff;')
  assert.match(errorsOf(broken), /gateway path must stay direct Go/)
})

test('warns but does not fail when non_idempotent is added', () => {
  const withNonIdempotent = TEMPLATE.replace(
    'proxy_next_upstream error timeout http_502 http_503 http_504;',
    'proxy_next_upstream error timeout http_502 http_503 http_504 non_idempotent;',
  )
  const result = resultOf(withNonIdempotent)
  assert.equal(result.ok, true, result.errors.join('\n'))
  assert.match(result.warnings.join('\n'), /non_idempotent/)
})

test('rejects a missing retry contract', () => {
  const broken = TEMPLATE.replace(/\n\s*proxy_next_upstream error timeout http_502 http_503 http_504;/g, '')
  assert.match(errorsOf(broken), /missing proxy_next_upstream/)
})

test('rejects proxy_next_upstream_tries below two', () => {
  const broken = TEMPLATE.replaceAll('proxy_next_upstream_tries 2;', 'proxy_next_upstream_tries 1;')
  assert.match(errorsOf(broken), /tries must be >= 2/)
})

test('rejects missing keepalive and health-check parameters', () => {
  assert.match(errorsOf(TEMPLATE.replaceAll('    keepalive 64;\n', '')), /missing keepalive/)
  assert.match(errorsOf(TEMPLATE.replaceAll('max_fails=2 ', '')), /needs max_fails/)
})

test('preserves BFF forwarded headers in every 3187 dispatch location', () => {
  const result = resultOf(TEMPLATE)
  assert.equal(result.ok, true, result.errors.join('\n'))
  const dispatch = result.servers.find((server) => server.listens.includes('127.0.0.1:3187'))
  assert.ok(dispatch, '3187 dispatch server must exist')
  for (const location of dispatch.locations) {
    assert.match(location.body, /proxy_set_header Host \$http_x_forwarded_host;/)
    assert.match(location.body, /proxy_set_header X-Forwarded-Host \$http_x_forwarded_host;/)
    assert.match(location.body, /proxy_set_header X-Forwarded-Proto \$http_x_forwarded_proto;/)
    assert.match(location.body, /proxy_set_header X-Forwarded-For \$http_x_forwarded_for;/)
    assert.match(location.body, /proxy_set_header X-Real-IP \$http_x_real_ip;/)
  }
})

test('rejects dispatch header overrides that lose public Host, proto or client IP', () => {
  const cases = [
    ['Host $http_x_forwarded_host;', 'Host $host;', /Host \$host overwrites/],
    ['X-Forwarded-Proto $http_x_forwarded_proto;', 'X-Forwarded-Proto $scheme;', /X-Forwarded-Proto \$scheme overwrites/],
    ['X-Forwarded-For $http_x_forwarded_for;', 'X-Forwarded-For $remote_addr;', /X-Forwarded-For \$remote_addr overwrites/],
    ['X-Real-IP $http_x_real_ip;', 'X-Real-IP $remote_addr;', /X-Real-IP \$remote_addr overwrites/],
  ]
  for (const [from, to, pattern] of cases) {
    const broken = TEMPLATE.replaceAll(`proxy_set_header ${from}`, `proxy_set_header ${to}`)
    assert.match(errorsOf(broken), pattern)
  }
})

test('rejects a dispatch that drops X-Forwarded-Host or X-Forwarded-For', () => {
  const noForwardedHost = TEMPLATE.replaceAll(/\s*proxy_set_header X-Forwarded-Host \$http_x_forwarded_host;\n/g, '')
  assert.match(errorsOf(noForwardedHost), /must forward X-Forwarded-Host/)
  const noForwardedFor = TEMPLATE.replaceAll(/\s*proxy_set_header X-Forwarded-For \$http_x_forwarded_for;\n/g, '')
  assert.match(errorsOf(noForwardedFor), /must forward X-Forwarded-For/)
})
test('CLI checks the dispatch and NUXT_BACKEND_ORIGIN together', () => {
  const run = spawnSync(process.execPath, [CHECKER_PATH, 'check', TEMPLATE_PATH, '--env', ENV_PATH], { encoding: 'utf8' })
  assert.equal(run.status, 0, run.stderr)
  assert.match(run.stdout, /dispatch sub2api_go_bff 2 servers on 127\.0\.0\.1:3187, origin http:\/\/127\.0\.0\.1:3187/)
})
