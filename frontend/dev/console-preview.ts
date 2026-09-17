import type { IncomingMessage, ServerResponse } from 'node:http'
import { existsSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import vue from '@vitejs/plugin-vue'
import type { InlineConfig, Plugin } from 'vite'
import { DEMO_TOKEN, PREVIEW_LABEL, settings } from './fixtures'
import { createMockApi, PreviewError } from './mock-api'

export const PREVIEW_MODE = 'console-preview'
const BODY_LIMIT = 64 * 1024

function send(res: ServerResponse, status: number, body: unknown) {
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' })
  res.end(JSON.stringify(body))
}

export function isLocalRequest(req: IncomingMessage) {
  const address = req.socket.remoteAddress
  const host = `127.0.0.1:${req.socket.localPort}`
  if (address !== '127.0.0.1' && address !== '::ffff:127.0.0.1') return false
  if (req.headers.host !== host) return false
  if (req.headers.origin && req.headers.origin !== `http://${host}`) return false
  if (req.headers['sec-fetch-site'] === 'cross-site') return false
  return !req.headers.forwarded && !req.headers['x-forwarded-host'] && !req.headers['x-forwarded-for']
}

async function readBody(req: IncomingMessage): Promise<Record<string, unknown>> {
  if (!['POST', 'PUT', 'PATCH'].includes(req.method || '')) return {}
  if (Number(req.headers['content-length'] || 0) > BODY_LIMIT) throw new PreviewError(413, '演示请求体过大')
  const chunks: Buffer[] = []
  let size = 0
  for await (const chunk of req) {
    size += Buffer.byteLength(chunk)
    if (size > BODY_LIMIT) throw new PreviewError(413, '演示请求体过大')
    chunks.push(Buffer.from(chunk))
  }
  if (!size) return {}
  if (!req.headers['content-type']?.startsWith('application/json')) throw new PreviewError(415, '演示接口仅接受 JSON')
  try {
    const body: unknown = JSON.parse(Buffer.concat(chunks).toString('utf8'))
    if (!body || typeof body !== 'object' || Array.isArray(body)) throw new Error('object required')
    return body as Record<string, unknown>
  } catch { throw new PreviewError(400, '无效的 JSON 请求体') }
}

export function consolePreviewPlugin(): Plugin {
  const api = createMockApi()
  let active = false
  return {
    name: 'local-console-preview',
    apply: 'serve',
    enforce: 'pre',
    configResolved(config) {
      active = config.command === 'serve' && config.mode === PREVIEW_MODE && !config.isPreview
      if (!active) return
      if (config.server.host !== '127.0.0.1' || config.server.https || config.server.proxy ||
          config.server.cors !== false || config.server.strictPort !== true || config.server.hmr === false) {
        throw new Error('Console preview requires HTTP on 127.0.0.1 with strictPort, no CORS or proxy')
      }
    },
    configureServer(server) {
      if (!active) return
      // Vite handles upgrades outside Connect; apply the same boundary to HMR.
      server.httpServer?.prependListener('upgrade', (req, socket) => {
        if (!isLocalRequest(req) || req.headers['sec-websocket-protocol'] !== 'vite-hmr') socket.destroy()
      })
      server.middlewares.use((req, res, next) => {
        if (!isLocalRequest(req)) {
          send(res, 403, { code: 403, message: '仅允许本机同源预览请求' })
          return
        }
        const host = req.headers.host!
        res.setHeader('Content-Security-Policy', [
          "default-src 'self'", "script-src 'self' 'unsafe-inline'", "style-src 'self' 'unsafe-inline'",
          "img-src 'self' data: blob:", "font-src 'self' data:",
          `connect-src 'self' ws://${host}`, "object-src 'none'", "frame-src 'none'",
          "frame-ancestors 'none'", "base-uri 'self'", "form-action 'self'",
        ].join('; '))
        res.setHeader('X-Content-Type-Options', 'nosniff')
        res.setHeader('Referrer-Policy', 'no-referrer')
        res.setHeader('X-Console-Preview', 'local-demo')
        res.setHeader('Cache-Control', 'no-store')
        let url: URL
        let path: string
        try {
          if (!req.url?.startsWith('/') || req.url.startsWith('//') || req.url.includes('\\')) throw new Error('invalid path')
          url = new URL(req.url, `http://${host}`)
          path = decodeURIComponent(url.pathname).replace(/\/+$/, '') || '/'
        } catch {
          send(res, 400, { code: 400, message: '无效的预览请求地址' })
          return
        }
        if (path === '/') {
          res.writeHead(302, { Location: '/dashboard' })
          res.end()
          return
        }
        if (!/^\/(api|v1|setup)(\/|$)/i.test(path)) { next(); return }
        void (async () => {
          try {
            const body = await readBody(req)
            let result = api.handle(req.method || 'GET', path, url.searchParams, body)
            if (path === '/api/v1/settings/public') result = { ...settings, api_base_url: `http://${host}` }
            const raw = path === '/setup/status' || path.startsWith('/v1/')
            send(res, 200, raw ? result : { code: 0, message: '本地演示数据', data: result })
          } catch (error) {
            const status = error instanceof PreviewError ? error.status : 500
            send(res, status, { code: status, message: error instanceof PreviewError ? error.message : '演示请求处理失败' })
          }
        })()
      })
    },
    transformIndexHtml: {
      order: 'pre',
      handler(html) {
        if (!active) return html
        const json = (value: unknown) => JSON.stringify(value).replace(/</g, '\\u003c')
        return {
          html: html.replace(/<title>[^<]*<\/title>/i, `<title>kedaya.ai · ${PREVIEW_LABEL}</title>`),
          tags: [{ tag: 'script', injectTo: 'head-prepend', children: `
            window.__APP_CONFIG__ = ${json(settings)};
            window.__APP_CONFIG__.api_base_url = location.origin;
            localStorage.setItem('auth_token', ${json(DEMO_TOKEN)});
            localStorage.setItem('auth_user', ${json(JSON.stringify(api.user))});
            localStorage.removeItem('refresh_token');
            localStorage.removeItem('token_expires_at');
            localStorage.removeItem('pending_auth_session');
            if (!localStorage.getItem('theme')) localStorage.setItem('theme', 'dark');
            if (!localStorage.getItem('sub2api_locale')) localStorage.setItem('sub2api_locale', 'zh');
            localStorage.setItem('user_guide_${api.user.id}_user_v4_interactive', 'true');
            document.documentElement.classList.toggle('dark', localStorage.getItem('theme') === 'dark');
            document.documentElement.lang = localStorage.getItem('sub2api_locale');
            document.addEventListener('DOMContentLoaded', function() {
              function labelTitle() {
                if (!document.title.includes(${json(PREVIEW_LABEL)})) document.title += ' · ' + ${json(PREVIEW_LABEL)};
              }
              labelTitle();
              new MutationObserver(labelTitle).observe(document.querySelector('title'), { childList: true });
            }, { once: true });
            document.addEventListener('click', function(event) {
              var anchor = event.target instanceof Element ? event.target.closest('a[href]') : null;
              if (anchor && new URL(anchor.href, location.href).origin !== location.origin) {
                event.preventDefault(); event.stopImmediatePropagation();
              }
            }, true);
          ` }],
        }
      },
    },
  }
}

export function consolePreviewConfig(): InlineConfig {
  const root = fileURLToPath(new URL('../', import.meta.url))
  const emptyEnvDir = resolve(root, 'dev/.no-env-files')
  if (existsSync(emptyEnvDir)) throw new Error('Reserved preview env directory must not exist')
  return {
    root,
    envFile: false,
    // Vite 5 only honors envFile from inline config; also protect direct CLI use.
    envDir: emptyEnvDir,
    // Do not expose inherited VITE_* variables in this isolated mode either.
    envPrefix: '__CONSOLE_PREVIEW_NO_ENV__',
    cacheDir: resolve(root, 'dev/.cache/vite'),
    plugins: [consolePreviewPlugin(), vue()],
    resolve: { alias: { '@': resolve(root, 'src'), 'vue-i18n': 'vue-i18n/dist/vue-i18n.runtime.esm-bundler.js' } },
    define: { __INTLIFY_JIT_COMPILATION__: true, 'import.meta.env.VITE_API_BASE_URL': JSON.stringify('/api/v1'),
      'import.meta.env.VITE_WS_BASE_URL': 'undefined', 'import.meta.env.VITE_INFINITE_CANVAS_URL': 'undefined' },
    server: { host: '127.0.0.1', port: 4317, strictPort: true, cors: false,
      fs: { strict: true, allow: [root] } },
  }
}
