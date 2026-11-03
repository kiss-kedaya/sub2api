#!/usr/bin/env node
// Nginx HA checker for the Nuxt SSR/BFF deployment.
//
// The incident this guards against is the *dispatch* hop:
//
//   SSR 3118 --NUXT_BACKEND_ORIGIN--> 127.0.0.1:3187
//   3187 nginx --hardcoded single Go backend--> 502
//
// The 3187 dispatcher must proxy to an upstream with at least two same-version
// Go backends (local primary + peer backup). The outer Node BFF upstream is
// optional HA; it is not the root-cause fix for this incident.
//
// The checker also verifies that SSR's NUXT_BACKEND_ORIGIN points at the local
// dispatch (3187), not directly at a Go port, when --env is supplied.
//
// Usage:
//   node deploy/nuxt/check-bff-ha.mjs check deploy/nuxt/nginx-ssr.example.conf \
//     --env deploy/nuxt/ssr.env.example
//   sudo nginx -T > /tmp/nginx.conf
//   node deploy/nuxt/check-bff-ha.mjs check /tmp/nginx.conf
//
// No dependencies; the test file imports validateBffHaConfig directly.

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

export const DEFAULT_BFF_UPSTREAM = 'sub2api_bff'
export const DEFAULT_DISPATCH_UPSTREAM = 'sub2api_go_bff'
export const DEFAULT_DISPATCH_LISTEN = '127.0.0.1:3187'
export const DEFAULT_BFF_PORTS = [3118, 3119]

const GATEWAY_PATHS = new Set([
  '/v1', '/v1/', '/v1beta', '/v1beta/', '/backend-api/', '/antigravity/',
  '/images/', '/videos/', '/responses', '/models', '/messages',
  '/chat/completions', '/embeddings', '/alpha/search', '/tts', '/stt',
  '/realtime', '/web_search', '/x_search',
])

/** Remove nginx comments while preserving newlines and quoted strings. */
export function stripComments(text) {
  let out = ''
  let quote = null
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if (quote) {
      out += ch
      if (ch === '\\' && quote === '"') {
        out += text[++i] ?? ''
        continue
      }
      if (ch === quote) quote = null
      continue
    }
    if (ch === '"' || ch === "'") {
      quote = ch
      out += ch
      continue
    }
    if (ch === '#') {
      while (i < text.length && text[i] !== '\n') i++
      out += '\n'
      continue
    }
    out += ch
  }
  return out
}

function matchBrace(text, openIndex) {
  let depth = 0
  let quote = null
  for (let i = openIndex; i < text.length; i++) {
    const ch = text[i]
    if (quote) {
      if (ch === '\\' && quote === '"') {
        i++
        continue
      }
      if (ch === quote) quote = null
      continue
    }
    if (ch === '"' || ch === "'") {
      quote = ch
      continue
    }
    if (ch === '{') depth++
    else if (ch === '}') {
      depth--
      if (depth === 0) return i
    }
  }
  return -1
}

function splitDirectives(body) {
  const out = []
  let current = ''
  let quote = null
  for (let i = 0; i < body.length; i++) {
    const ch = body[i]
    if (quote) {
      current += ch
      if (ch === '\\' && quote === '"') {
        current += body[++i] ?? ''
        continue
      }
      if (ch === quote) quote = null
      continue
    }
    if (ch === '"' || ch === "'") {
      quote = ch
      current += ch
      continue
    }
    if (ch === ';') {
      if (current.trim()) out.push(current.trim())
      current = ''
      continue
    }
    current += ch
  }
  if (current.trim()) out.push(current.trim())
  return out
}

export function parseUpstreams(text) {
  const clean = stripComments(text)
  const upstreams = []
  const re = /\bupstream\s+([A-Za-z0-9_]+)\s*\{/g
  let match
  while ((match = re.exec(clean))) {
    const open = match.index + match[0].length - 1
    const close = matchBrace(clean, open)
    if (close === -1) throw new Error(`unbalanced upstream ${match[1]}`)
    const body = clean.slice(open + 1, close)
    const directives = splitDirectives(body)
    const servers = []
    for (const directive of directives) {
      const serverMatch = /^server\s+(.+)$/s.exec(directive)
      if (!serverMatch) continue
      const parts = serverMatch[1].trim().split(/\s+/)
      const address = parts.shift()
      const params = {}
      for (const part of parts) {
        const eq = part.indexOf('=')
        if (eq === -1) params[part] = true
        else params[part.slice(0, eq)] = part.slice(eq + 1)
      }
      servers.push({ address, params })
    }
    upstreams.push({ name: match[1], body, directives, servers })
    re.lastIndex = close + 1
  }
  return upstreams
}

export function parseLocations(text) {
  const clean = stripComments(text)
  const locations = []
  const re = /\blocation\s+(?:(=|\^~|~\*|~)\s+)?([^\s{]+)\s*\{/g
  let match
  while ((match = re.exec(clean))) {
    const open = match.index + match[0].length - 1
    const close = matchBrace(clean, open)
    if (close === -1) throw new Error(`unbalanced location ${match[2]}`)
    locations.push({
      modifier: match[1] || '',
      path: match[2],
      body: clean.slice(open + 1, close),
    })
    re.lastIndex = close + 1
  }
  return locations
}

export function parseServers(text) {
  const clean = stripComments(text)
  const servers = []
  const re = /\bserver\s*\{/g
  let match
  while ((match = re.exec(clean))) {
    const open = match.index + match[0].length - 1
    const close = matchBrace(clean, open)
    if (close === -1) throw new Error('unbalanced server block')
    const body = clean.slice(open + 1, close)
    const listens = []
    const listenRe = /\blisten\s+([^;]+);/g
    let listenMatch
    while ((listenMatch = listenRe.exec(body))) {
      const address = listenMatch[1].trim().split(/\s+/)[0]
      if (address) listens.push(address)
    }
    servers.push({ body, listens, locations: parseLocations(body) })
    re.lastIndex = close + 1
  }
  return servers
}

export function isBffPath(path) {
  const p = path.replace(/\?.*$/, '')
  return p === '/'
    || p === '/api/v1'
    || p.startsWith('/api/v1/')
    || p === '/health/frontend'
    || p.startsWith('/_nuxt/')
}

export function isDispatchPath(path) {
  const p = path.replace(/\?.*$/, '')
  return p === '/api/v1' || p.startsWith('/api/v1/')
}

export function proxyPassTarget(body) {
  const match = /\bproxy_pass\s+([^;]+);/.exec(body)
  if (!match) return null
  let target = match[1].trim()
  if ((target.startsWith('"') && target.endsWith('"')) || (target.startsWith("'") && target.endsWith("'"))) {
    target = target.slice(1, -1)
  }
  target = target.replace(/^https?:\/\//i, '')
  const slash = target.indexOf('/')
  if (slash !== -1) target = target.slice(0, slash)
  return target
}

function parseAddress(address) {
  if (!address || address.startsWith('unix:')) return null
  const ipv6 = /^\[([^\]]+)\]:(\d+)$/.exec(address)
  if (ipv6) return { host: ipv6[1], port: Number(ipv6[2]) }
  const match = /^([^:]+):(\d+)$/.exec(address)
  if (!match) return null
  return { host: match[1], port: Number(match[2]) }
}

function isPositiveNginxTime(value) {
  return typeof value === 'string' && /^\d+(?:ms|s|m|h|d)?$/.test(value) && Number.parseFloat(value) > 0
}

function locationLabel(location) {
  return `${location.modifier ? `${location.modifier} ` : ''}${location.path}`
}

function listenPort(address) {
  if (/^\d+$/.test(address)) return Number(address)
  const match = /:(\d+)$/.exec(address)
  return match ? Number(match[1]) : null
}

function listenMatches(server, target) {
  const port = listenPort(target)
  return server.listens.some((address) => listenPort(address) === port)
}

function isLoopbackListen(address, target) {
  const port = listenPort(target)
  return [`127.0.0.1:${port}`, `[::1]:${port}`, `localhost:${port}`].includes(address)
}

function validateUpstreamShape(upstream, options, errors, warnings) {
  const { label, minServers, requireBackup } = options
  const addresses = upstream.servers.map((server) => server.address)
  const addressesSet = new Set(addresses)
  if (upstream.servers.length < minServers) {
    const message = `${label} upstream ${upstream.name} has ${upstream.servers.length} server(s); need at least ${minServers}`
    if (options.failOnMissingHa) errors.push(message)
    else warnings.push(`${message}; outer Node HA is not the 3187 dispatch root-cause fix`)
  }
  if (addressesSet.size !== addresses.length) {
    errors.push(`${label} upstream ${upstream.name} has duplicate server addresses: ${addresses.join(', ')}`)
  }
  if (!/\bkeepalive\s+\d+\s*;/m.test(upstream.body)) {
    errors.push(`${label} upstream ${upstream.name} is missing keepalive; HTTP/1.1 connection reuse is required`)
  }
  const backups = upstream.servers.filter((server) => server.params.backup).length
  if (backups === upstream.servers.length) {
    errors.push(`${label} upstream ${upstream.name} marks every server as backup; at least one normal slot is required`)
  }
  if (requireBackup && backups === 0) {
    errors.push(`${label} upstream ${upstream.name} needs a peer backup; local primary + peer backup is required`)
  }
  for (const server of upstream.servers) {
    const parsed = parseAddress(server.address)
    if (!parsed) {
      errors.push(`${label} upstream ${upstream.name} server ${server.address} must be host:port (unix sockets are not supported by this template)`)
    } else if (!Number.isInteger(parsed.port) || parsed.port < 1 || parsed.port > 65535) {
      errors.push(`${label} upstream ${upstream.name} server ${server.address} has an invalid port`)
    }
    if (!server.params.max_fails || server.params.max_fails === true || !/^\d+$/.test(server.params.max_fails) || Number(server.params.max_fails) < 1) {
      errors.push(`${label} upstream ${upstream.name} server ${server.address} needs max_fails=<positive integer>`)
    }
    if (!isPositiveNginxTime(server.params.fail_timeout)) {
      errors.push(`${label} upstream ${upstream.name} server ${server.address} needs fail_timeout=<positive time>, e.g. 10s`)
    }
  }
  return addressesSet
}

function validateProxyContract(location, expectedUpstream, label, errors, warnings) {
  const target = proxyPassTarget(location.body)
  if (!target) {
    errors.push(`${label}: missing proxy_pass http://${expectedUpstream}`)
  } else if (target.includes('$')) {
    errors.push(`${label}: dynamic proxy_pass ${target} cannot be HA-validated; use upstream ${expectedUpstream}`)
  } else if (target !== expectedUpstream) {
    errors.push(`${label}: proxy_pass points at ${target}; use upstream ${expectedUpstream} (a literal backend is the single-backend 502 bug)`)
  }
  if (!/proxy_http_version\s+1\.1\s*;/.test(location.body)) {
    errors.push(`${label}: missing proxy_http_version 1.1`)
  }
  if (!/proxy_set_header\s+Connection\s+["']{2}\s*;/.test(location.body)) {
    errors.push(`${label}: missing proxy_set_header Connection ""; keepalive to upstream will not be reused`)
  }
  const nextUpstream = /\bproxy_next_upstream\s+([^;]+);/.exec(location.body)
  if (!nextUpstream) {
    errors.push(`${label}: missing proxy_next_upstream error timeout http_502 http_503 http_504`)
  } else {
    const tokens = nextUpstream[1].trim().split(/\s+/)
    if (!tokens.includes('error')) errors.push(`${label}: proxy_next_upstream must include error`)
    if (!tokens.some((token) => ['http_502', 'http_503', 'http_504', '502', '503', '504'].includes(token))) {
      errors.push(`${label}: proxy_next_upstream must include http_502/http_503/http_504`)
    }
    if (tokens.includes('non_idempotent')) {
      warnings.push(`${label}: non_idempotent retries can replay POST/login requests; leave it out for the BFF/dispatch`)
    }
  }
  const tries = /\bproxy_next_upstream_tries\s+(\d+)\s*;/.exec(location.body)
  if (!tries) {
    errors.push(`${label}: missing proxy_next_upstream_tries 2`)
  } else if (Number(tries[1]) < 2) {
    errors.push(`${label}: proxy_next_upstream_tries must be >= 2 to reach the other slot`)
  }
  if (!/proxy_connect_timeout\s+\d+(?:\.\d+)?(?:ms|s|m)?\s*;/.test(location.body)) {
    warnings.push(`${label}: missing proxy_connect_timeout; a dead slot can hold the request until the default timeout`)
  }
}

function validateBffLocations(locations, upstreamName, bffAddresses, errors, warnings) {
  const bffLocations = locations.filter((location) => isBffPath(location.path))
  if (bffLocations.length === 0) return 0
  if (!bffLocations.some((location) => location.path === '/')) {
    warnings.push('outer BFF: no location / catch-all; SSR pages outside /api/v1/ may 404')
  }
  if (!bffLocations.some((location) => location.path === '/api/v1/')) {
    warnings.push('outer BFF: no location ^~ /api/v1/ prefix block; subpaths may fall into the page catch-all')
  }
  for (const location of bffLocations) {
    const label = `outer BFF ${locationLabel(location)}`
    validateProxyContract(location, upstreamName, label, errors, warnings)
    const target = proxyPassTarget(location.body)
    if (target && bffAddresses.has(target)) {
      errors.push(`${label}: proxy_pass ${target} bypasses upstream ${upstreamName}; use the upstream name`)
    }
  }
  return bffLocations.length
}

function hasProxyHeader(body, header, value) {
  const escaped = value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return new RegExp(`\\bproxy_set_header\\s+${header}\\s+${escaped}\\s*;`, 'i').test(body)
}

function validateDispatchForwarding(location, label, errors) {
  const required = [
    ['Host', '$http_x_forwarded_host'],
    ['X-Forwarded-Host', '$http_x_forwarded_host'],
    ['X-Forwarded-Proto', '$http_x_forwarded_proto'],
    ['X-Forwarded-For', '$http_x_forwarded_for'],
    ['X-Real-IP', '$http_x_real_ip'],
  ]
  for (const [header, value] of required) {
    if (!hasProxyHeader(location.body, header, value)) {
      errors.push(`${label}: must forward ${header} ${value} (BFF already sanitized it; do not overwrite)`)
    }
  }
  const overrides = [
    ['Host', '$host'],
    ['X-Forwarded-Host', '$host'],
    ['X-Forwarded-Proto', '$scheme'],
    ['X-Forwarded-For', '$remote_addr'],
    ['X-Real-IP', '$remote_addr'],
  ]
  for (const [header, value] of overrides) {
    if (hasProxyHeader(location.body, header, value)) {
      errors.push(`${label}: ${header} ${value} overwrites the BFF value; use the forwarded header`)
    }
  }
}

function validateDispatchLocations(locations, upstreamName, dispatchAddresses, errors, warnings) {
  let dispatchLocations = 0
  for (const location of locations) {
    const label = `3187 dispatch ${locationLabel(location)}`
    const target = proxyPassTarget(location.body)
    if (GATEWAY_PATHS.has(location.path)) {
      if (target === upstreamName) {
        errors.push(`${label}: gateway path must stay direct Go on the outer nginx, not the 3187 dispatch`)
      }
      continue
    }
    if (isDispatchPath(location.path) || target === upstreamName) {
      dispatchLocations++
      validateProxyContract(location, upstreamName, label, errors, warnings)
      validateDispatchForwarding(location, label, errors)
      if (target && dispatchAddresses.has(target)) {
        errors.push(`${label}: proxy_pass ${target} bypasses upstream ${upstreamName}; use the upstream name`)
      }
      continue
    }
    if (target === upstreamName) {
      warnings.push(`${label}: 3187 dispatch should only serve /api/v1; this extra path also proxies to ${upstreamName}`)
    }
  }
  if (dispatchLocations === 0) {
    errors.push('3187 dispatch has no /api/v1 location; SSR NUXT_BACKEND_ORIGIN would have no Go upstream')
  }
  return dispatchLocations
}

/**
 * Validate a combined nginx config (or the template file).
 * Returns { ok, errors, warnings, upstream, dispatchUpstream, servers, locations, dispatchLocations }.
 */
export function validateBffHaConfig(text, options = {}) {
  const bffUpstreamName = options.upstream || DEFAULT_BFF_UPSTREAM
  const dispatchUpstreamName = options.dispatchUpstream || DEFAULT_DISPATCH_UPSTREAM
  const dispatchListen = options.dispatchListen || DEFAULT_DISPATCH_LISTEN
  const minServers = options.minServers ?? 2
  const requireBffHa = options.requireBffHa === true
  const backendOrigin = options.backendOrigin
  const errors = []
  const warnings = []

  const upstreams = parseUpstreams(text)
  const servers = parseServers(text)
  const allLocations = parseLocations(text)
  const outerServers = servers.filter((server) => !listenMatches(server, dispatchListen))
  const dispatchServers = servers.filter((server) => listenMatches(server, dispatchListen))

  // Outer Node BFF upstream: optional HA. It is not this incident's root cause.
  const bff = upstreams.find((upstream) => upstream.name === bffUpstreamName)
  let bffAddresses = new Set()
  let bffLocationCount = 0
  if (bff) {
    bffAddresses = validateUpstreamShape(bff, {
      label: 'outer BFF',
      minServers,
      requireBackup: false,
      failOnMissingHa: requireBffHa,
    }, errors, warnings)
    if (outerServers.length === 0) {
      warnings.push(`outer BFF upstream ${bffUpstreamName} exists but no outer server block was found`)
    } else {
      for (const server of outerServers) {
        bffLocationCount += validateBffLocations(server.locations, bffUpstreamName, bffAddresses, errors, warnings)
      }
    }
  } else {
    warnings.push(`outer BFF upstream ${bffUpstreamName} not found; checking the 3187 dispatch only (outer Node HA is not the root-cause fix)`)
  }

  // 3187 dispatch: required, at least two same-version Go backends.
  const dispatch = upstreams.find((upstream) => upstream.name === dispatchUpstreamName)
  let dispatchAddresses = new Set()
  if (!dispatch) {
    errors.push(`missing dispatch upstream ${dispatchUpstreamName}: 3187 would fall back to a single Go backend`)
  } else {
    dispatchAddresses = validateUpstreamShape(dispatch, {
      label: '3187 dispatch',
      minServers,
      requireBackup: true,
      failOnMissingHa: true,
    }, errors, warnings)
  }

  if (dispatchServers.length === 0) {
    errors.push(`missing server listen ${dispatchListen}: SSR NUXT_BACKEND_ORIGIN has no stable local dispatch`)
  } else {
    if (dispatchServers.length > 1) {
      errors.push(`duplicate dispatch server for ${dispatchListen}; keep exactly one local dispatch listener`)
    }
    for (const server of dispatchServers) {
      if (!server.listens.some((address) => isLoopbackListen(address, dispatchListen))) {
        errors.push(`3187 dispatch must bind ${dispatchListen} (loopback); found ${server.listens.join(', ') || '(no listen)'}`)
      }
      validateDispatchLocations(server.locations, dispatchUpstreamName, dispatchAddresses, errors, warnings)
    }
  }

  // Gateway/SSE/WebSocket traffic must stay direct on the outer nginx.
  for (const server of servers) {
    for (const location of server.locations) {
      if (!GATEWAY_PATHS.has(location.path)) continue
      const target = proxyPassTarget(location.body)
      if (target === bffUpstreamName || target === dispatchUpstreamName) {
        errors.push(`${locationLabel(location)}: gateway path must stay direct Go, not ${target}`)
      }
    }
  }

  if (backendOrigin !== undefined) {
    const expected = `http://${dispatchListen}`
    const actual = String(backendOrigin).trim().replace(/\/+$/, '')
    if (actual !== expected) {
      errors.push(`NUXT_BACKEND_ORIGIN=${actual} must point at ${expected} (the stable local dispatch), not at a Go port`)
    }
  } else {
    warnings.push('NUXT_BACKEND_ORIGIN not checked; pass --env deploy/nuxt/ssr.env.example')
  }

  return {
    ok: errors.length === 0,
    errors: [...new Set(errors)],
    warnings: [...new Set(warnings)],
    upstream: bff,
    dispatchUpstream: dispatch,
    servers,
    locations: allLocations,
    bffLocations: bffLocationCount,
    dispatchLocations: dispatchServers.length,
  }
}

export function readBackendOrigin(text) {
  const line = text.split(/\r?\n/).find((entry) => /^\s*NUXT_BACKEND_ORIGIN\s*=/.test(entry))
  if (!line) return null
  let value = line.slice(line.indexOf('=') + 1).trim()
  value = value.replace(/\s+#.*$/, '').trim()
  if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
    value = value.slice(1, -1)
  }
  return value
}

function readFiles(files) {
  return files.map((file) => {
    try {
      return readFileSync(resolve(process.cwd(), file), 'utf8')
    } catch (error) {
      throw new Error(`cannot read ${file}: ${error.message}`)
    }
  }).join('\n')
}

function usage(exitCode = 2) {
  console.error(`Usage:
  node deploy/nuxt/check-bff-ha.mjs check <config...> [--env ssr.env] [--upstream NAME]
        [--dispatch-upstream NAME] [--dispatch-listen 127.0.0.1:3187]
        [--min-servers N] [--require-bff-ha]

check accepts several files (snippets concatenated) or one expanded file
produced by: sudo nginx -T > /tmp/nginx.conf`)
  process.exit(exitCode)
}

function main(argv) {
  if (argv[0] !== 'check') usage()
  const files = []
  const options = {}
  let envFile
  for (let i = 1; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--env') envFile = argv[++i]
    else if (arg === '--upstream') options.upstream = argv[++i]
    else if (arg === '--dispatch-upstream') options.dispatchUpstream = argv[++i]
    else if (arg === '--dispatch-listen') options.dispatchListen = argv[++i]
    else if (arg === '--min-servers') options.minServers = Number(argv[++i])
    else if (arg === '--require-bff-ha') options.requireBffHa = true
    else if (arg.startsWith('-')) usage()
    else files.push(arg)
  }
  if (files.length === 0) usage()
  if (envFile) {
    const origin = readBackendOrigin(readFiles([envFile]))
    if (origin === null) throw new Error(`NUXT_BACKEND_ORIGIN not found in ${envFile}`)
    options.backendOrigin = origin
  }
  const result = validateBffHaConfig(readFiles(files), options)
  for (const warning of result.warnings) console.warn(`warn: ${warning}`)
  for (const error of result.errors) console.error(`error: ${error}`)
  if (!result.ok) {
    console.error(`FAIL ${files.join(' + ')}`)
    process.exit(1)
  }
  const bffCount = result.upstream?.servers.length ?? 0
  const dispatchCount = result.dispatchUpstream?.servers.length ?? 0
  const origin = options.backendOrigin ? `, origin ${options.backendOrigin}` : ''
  console.log(`OK ${files.join(' + ')}: outer BFF ${bffCount} servers, dispatch ${options.dispatchUpstream || DEFAULT_DISPATCH_UPSTREAM} ${dispatchCount} servers on ${options.dispatchListen || DEFAULT_DISPATCH_LISTEN}${origin}`)
}

const isMain = process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url
if (isMain) {
  try {
    main(process.argv.slice(2))
  } catch (error) {
    console.error(`error: ${error.message}`)
    process.exit(2)
  }
}
