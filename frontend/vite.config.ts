import { defineConfig, loadEnv, Plugin, searchForWorkspaceRoot } from 'vite'
import vue from '@vitejs/plugin-vue'
import ui from '@nuxt/ui/vite'
import checker from 'vite-plugin-checker'
import { resolve } from 'path'
import { nuxtUiOptions } from './nuxt-ui.config'

function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  })[character] || character)
}

function isSafeImageUrl(value: string): boolean {
  const trimmed = value.trim()
  if ((trimmed.startsWith('/') && !trimmed.startsWith('//')) || /^data:image\//i.test(trimmed)) {
    return true
  }
  try {
    const parsed = new URL(trimmed)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
  } catch {
    return false
  }
}

function injectBranding(html: string, config: { site_name?: string; site_logo?: string }): string {
  let brandedHtml = html
  const siteName = config.site_name?.trim()
  if (siteName) {
    brandedHtml = brandedHtml.replace(
      /<title>[^<]*<\/title>/i,
      `<title>${escapeHtml(siteName)} - AI API Gateway</title>`,
    )
  }

  const siteLogo = config.site_logo?.trim()
  if (siteLogo && isSafeImageUrl(siteLogo)) {
    brandedHtml = brandedHtml.replace(
      /<link\s+rel=["']icon["'][^>]*>/i,
      `<link rel="icon" href="${escapeHtml(siteLogo)}" />`,
    )
  }
  return brandedHtml
}

/**
 * Vite 插件：开发模式下注入公开配置到 index.html
 * 与生产模式的后端注入行为保持一致，消除闪烁
 */
function injectPublicSettings(backendUrl: string): Plugin {
  return {
    name: 'inject-public-settings',
    apply: 'serve',
    transformIndexHtml: {
      order: 'pre',
      async handler(html) {
        try {
          const response = await fetch(`${backendUrl}/api/v1/settings/public`, {
            signal: AbortSignal.timeout(2000)
          })
          if (response.ok) {
            const data = await response.json()
            if (data.code === 0 && data.data) {
              const script = `<script>window.__APP_CONFIG__=${JSON.stringify(data.data)};</script>`
              return injectBranding(html, data.data).replace('</head>', `${script}\n</head>`)
            }
          }
        } catch (e) {
          console.warn('[vite] 无法获取公开配置，将回退到 API 调用:', (e as Error).message)
        }
        return html
      }
    }
  }
}

/**
 * Which modules does a FIRST page load have to download? Those the HTML entry reaches through static imports only; anything
 * behind a dynamic `import()` (every route component, the Airwallex / Stripe / xlsx SDKs, modal bodies...) is fetched later.
 *
 * Used by `manualChunks` below to keep third-party code out of the one shared chunk every visitor downloads unless the first
 * page needs it. The answer is computed once, from the module graph Rollup has already built.
 */
export type ChunkContext = {
  getModuleInfo: (id: string) => { isEntry: boolean; importedIds: readonly string[] } | null
  getModuleIds: () => IterableIterator<string>
}

export function createBootGraph() {
  let boot: Set<string> | undefined
  return (id: string, ctx: ChunkContext): boolean => {
    if (!boot) {
      boot = new Set<string>()
      const queue: string[] = []
      // tsconfig.node.json has no `target`, so `for...of` over an iterator does not type-check here: walk it by hand
      const ids = ctx.getModuleIds()
      for (let item = ids.next(); !item.done; item = ids.next()) {
        if (ctx.getModuleInfo(item.value)?.isEntry) queue.push(item.value)
      }
      while (queue.length > 0) {
        const current = queue.pop() as string
        if (boot.has(current)) continue
        boot.add(current)
        for (const next of ctx.getModuleInfo(current)?.importedIds ?? []) queue.push(next)
      }
    }
    return boot.has(id)
  }
}

const isLoadedAtBoot = createBootGraph()

export default defineConfig(({ mode }) => {
  if (mode === 'console-preview') {
    throw new Error('console-preview is local dev only; use pnpm run dev:console')
  }

  // 加载环境变量
  const env = loadEnv(mode, process.cwd(), '')
  const backendUrl = env.VITE_DEV_PROXY_TARGET || 'http://localhost:8080'
  const devPort = Number(env.VITE_DEV_PORT || 3000)

  return {
    plugins: [
      vue(),
      // Nuxt UI in Vue mode; also registers @tailwindcss/vite. Light/dark stays with the app
      // (`.dark` on <html> from localStorage `theme`), so Nuxt UI's color-mode plugin is off.
      // Icons are bundled from @iconify-json/* at build time (see src/plugins/nuxtUi.ts).
      // The theme (incl. the switch state matrix) is shared with vitest.config.ts via
      // nuxt-ui.config.ts, so specs assert the classes production actually ships.
      ui(nuxtUiOptions),
      checker({
        vueTsc: true
      }),
      injectPublicSettings(backendUrl)
    ],
  resolve: {
    alias: {
      '@runtime/context': resolve(__dirname, 'src/runtime/context.ts'),
      '@': resolve(__dirname, 'src'),
      // 使用 vue-i18n 运行时版本，避免 CSP unsafe-eval 问题
      'vue-i18n': 'vue-i18n/dist/vue-i18n.runtime.esm-bundler.js'
    }
  },
  define: {
    // 启用 vue-i18n JIT 编译，在 CSP 环境下处理消息插值
    // JIT 编译器生成 AST 对象而非 JS 代码，无需 unsafe-eval
    __INTLIFY_JIT_COMPILATION__: true
  },
  build: {
    outDir: '../backend/internal/web/dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        /**
         * 手动分包配置
         * 分离第三方库并按功能合并应用代码，避免循环依赖
         */
        manualChunks(id: string, ctx: ChunkContext) {
          if (id.includes('node_modules')) {
            // Vue 核心库。按包根目录匹配：`/vue/` 子串还会命中 @iconify/vue、@unhead/vue
            // 和 @nuxt/ui 的 runtime/vue/，把它们拆进这个块后与 vendor-misc 互相引用，
            // 首屏报 "Cannot access before initialization"。
            if (/[\\/]node_modules[\\/](vue|vue-router|pinia|@vue[\\/][^\\/]+)[\\/]/.test(id)) {
              return 'vendor-vue'
            }

            // xlsx 已改为业务里 await import() 懒加载，必须单独成块。
            // 和 @vueuse 合在一起会把 390KB 的表格库拖进首屏（vueuse 是首屏依赖）。
            if (id.includes('/xlsx/')) {
              return 'vendor-xlsx'
            }

            // UI 工具库
            if (id.includes('/@vueuse/')) {
              return 'vendor-ui'
            }

            // 图表库
            if (id.includes('/chart.js/') || id.includes('/vue-chartjs/')) {
              return 'vendor-chart'
            }

            // 国际化
            if (id.includes('/vue-i18n/') || id.includes('/@intlify/')) {
              return 'vendor-i18n'
            }

            // Stripe 仅在支付流程中按需加载，避免进入首页公共依赖。
            if (id.includes('/@stripe/stripe-js/')) {
              return 'vendor-stripe'
            }

            // Airwallex SDK 只在 /payment/airwallex 里 `await import()` 懒加载。不单列的话它会落进必载的
            // vendor-misc，模块顶层代码随首屏一起执行：每个用户打开任何页面都会去 static.airwallex.com 和
            // checkout.airwallex.com 多拉两份脚本（约 280 KB）。@airwallex/airtracker 是它的依赖，一起走。
            if (id.includes('/@airwallex/')) {
              return 'vendor-airwallex'
            }

            // 其他第三方库：只有「首屏会静态加载到」的才合进共享的 vendor-misc（哈希只随依赖变化，回访用户命中缓存）。
            // 以前这里无条件合并，等于把所有路由（管理后台、签名工具、日期选择、二维码……）用到的第三方模块并集，
            // 塞进每个访客打开任何页面都要下载的一个包；现在只被懒加载路由用到的模块留给 Rollup，跟着那些路由按需加载。
            if (!isLoadedAtBoot(id, ctx)) return undefined
            return 'vendor-misc'
          }

          // 应用代码：按入口点自动分包，不手动干预
          // 这样可以避免循环依赖，同时保持合理的 chunk 数量
        }
      }
    }
  },
    server: {
      host: '0.0.0.0',
      port: devPort,
      fs: {
        allow: [searchForWorkspaceRoot(process.cwd()), resolve(__dirname, '..')],
      },
      proxy: {
        '/api': {
          target: backendUrl,
          changeOrigin: true
        },
        '/v1': {
          target: backendUrl,
          changeOrigin: true
        },
        '/setup': {
          target: backendUrl,
          changeOrigin: true
        }
      }
    }
  }
})
