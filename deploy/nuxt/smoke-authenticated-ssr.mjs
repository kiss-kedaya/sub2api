#!/usr/bin/env node
// Run only in CI against an isolated bundle and an in-memory fixture backend.
// A login-page 200 must never stand in for an authenticated page render.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { resolve, join } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { createSession, sealBffSession } from '../../frontend/server/utils/session.ts'

const output = resolve(process.argv[2] || 'frontend/.output')
assert.ok(existsSync(join(output, 'server/index.mjs')), 'build the SSR bundle before this smoke')
const password = 'isolated-ssr-smoke-password-not-production'
const settings = {
  site_name: 'SSR fixture', version: 'fixture', backend_mode_enabled: false,
  custom_menu_items: [], custom_endpoints: [], registration_email_suffix_whitelist: [],
  login_agreement_enabled: false, passkey_enabled: false, payment_enabled: true,
  crypto_enabled: true, risk_control_enabled: true, channel_monitor_enabled: true,
  table_default_page_size: 20, table_page_size_options: [10, 20, 50, 100],
}
const user = {
  id: 1, username: 'fixture', email: 'fixture@example.test', role: 'admin',
  status: 'active', balance: 0, concurrency: 1, allowed_groups: [], subscriptions: [],
  balance_notify_enabled: false, balance_notify_threshold: null, balance_notify_extra_emails: [],
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', run_mode: 'standard',
}
const accessToken = 'fixture-access-only-not-production'
const backend = createServer((req, res) => {
  res.setHeader('content-type', 'application/json')
  const path = new URL(req.url, 'http://fixture.invalid').pathname
  if (!['GET', 'HEAD'].includes(req.method)) { res.writeHead(405); res.end('{}'); return }
  if (path !== '/api/v1/settings/public' && req.headers.authorization !== `Bearer ${accessToken}`) {
    res.writeHead(401); res.end(JSON.stringify({ code: 401, message: 'fixture auth required' })); return
  }
  const data = path === '/api/v1/settings/public' ? settings
    : path === '/api/v1/auth/me' ? user
      : path === '/api/v1/admin/compliance' ? { required: false, version: 'fixture' }
        : path === '/api/v1/admin/settings' ? settings : []
  res.end(JSON.stringify({ code: 0, message: 'success', data }))
})
async function listen(server) {
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  return server.address().port
}
const backendPort = await listen(backend)
const allocator = createServer()
const port = await listen(allocator)
await new Promise(resolve => allocator.close(resolve))
const session = createSession({ accessToken, refreshToken: '', expiresIn: 3600, uid: 1 })
const cookie = `sub2_session=${sealBffSession(session, password)}`
const child = spawn(process.execPath, [join(output, 'server/index.mjs')], {
  cwd: output, stdio: ['ignore', 'pipe', 'pipe'],
  env: { ...process.env, NODE_ENV: 'production', HOST: '127.0.0.1', NITRO_HOST: '127.0.0.1',
    PORT: String(port), NITRO_PORT: String(port), NUXT_BACKEND_ORIGIN: `http://127.0.0.1:${backendPort}`,
    NUXT_SESSION_PASSWORD: password, NUXT_SESSION_COOKIE_SECURE: 'false',
  },
})
const errors = []
for (const pipe of [child.stdout, child.stderr]) pipe.on('data', chunk => {
  errors.push(chunk.toString()); if (errors.length > 20) errors.shift()
})
const base = `http://127.0.0.1:${port}`
async function get(path, authenticated = true) {
  const response = await fetch(base + path, { redirect: 'manual', cache: 'no-store',
    headers: authenticated ? { cookie } : {}, signal: AbortSignal.timeout(20000) })
  return { status: response.status, type: response.headers.get('content-type') || '', body: await response.text() }
}
try {
  let ready = false
  for (let attempt = 0; attempt < 120; attempt++) {
    assert.equal(child.exitCode, null, 'isolated SSR child exited early')
    try { if ((await get('/health/frontend', false)).status === 200) { ready = true; break } } catch {}
    await delay(250)
  }
  assert.ok(ready, 'isolated SSR bundle did not become ready')
  const probe = await get('/api/v1/auth/session')
  assert.equal(JSON.parse(probe.body).data.authenticated, true, 'fixture cookie was not recognized')
  for (const path of ['/admin/dashboard', '/admin/accounts', '/admin/users', '/admin/groups',
    '/admin/proxies', '/usage', '/profile', '/key-usage', '/docs']) {
    const page = await get(path)
    assert.equal(page.status, 200, `${path}: authenticated render failed: ${page.body.slice(0, 250)}`)
    assert.ok(page.type.includes('text/html'), `${path}: did not render HTML`)
    assert.ok(page.body.includes('data-ssr="true"'), `${path}: client fallback is not an SSR pass`)
    assert.ok(!page.body.includes(accessToken), `${path}: bearer credential reached HTML`)
    console.log(`authenticated SSR ok: ${path} 200`)
  }
} catch (error) {
  console.error(errors.join('').slice(-8000))
  throw error
} finally {
  child.kill('SIGTERM')
  const deadline = Date.now() + 5000
  while (child.exitCode === null && child.signalCode === null && Date.now() < deadline) await delay(50)
  if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL')
  backend.closeAllConnections()
  await new Promise(resolve => backend.close(resolve))
}
