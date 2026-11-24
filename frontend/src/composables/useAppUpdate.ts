/**
 * 页面版本探测：这个标签页加载的前端比服务端当前的旧，就提示并强制刷新。
 *
 * 为什么需要：index.html 是 no-cache，一次普通刷新就能拿到新版本；但标签页一直开着不刷新，
 * 页面里的旧代码会继续调用已经升级过的接口（典型例子：创建 Key 加了身份校验，旧页面不带校验材料，
 * 被拒后只看到一句看不懂的报错）。
 *
 * 判定：
 * 1. 启动时记下「这个页面是哪个版本」——来自服务端注入进 HTML 的 window.__APP_CONFIG__.version，
 *    以及入口脚本的哈希（/assets/index-<hash>.js）。必须在 main.ts 刷新配置之前记下，所以在本模块
 *    被导入时就取一次。
 * 2. 定时（5 分钟）、切回标签页、恢复联网时，读一次 /settings/public 的版本号。
 * 3. 版本号不同时再读一次 `/` 的入口脚本哈希：只有前端文件真的变了才算「需要刷新」，
 *    纯后端发版不会打扰用户。读不到哈希时按版本号判断。
 * 4. 灰度（金丝雀）期间两台后端会给出不同版本：同一个「过期」结果隔一会儿再确认一次才算数。
 *
 * 刷新方式：
 * - 顶部常驻提示（AppUpdateBanner）；
 * - 下一次页面跳转时直接整页加载目标地址（不丢任何输入，因为用户本来就在离开当前页）；
 * - 创建 Key 之类的敏感操作前调用 ensureFresh()，过期就拦住并弹出必须刷新的对话框。
 * 自动刷新有 2 分钟冷却，避免服务端暂时给出不一致版本时反复重载。
 */
import { ref, type Ref } from 'vue'
import type { Router } from 'vue-router'
import { buildApiUrl } from '@/api/url'

export const POLL_INTERVAL_MS = 5 * 60_000
export const FIRST_CHECK_DELAY_MS = 60_000
export const FOCUS_RECHECK_MIN_MS = 30_000
export const CONFIRM_DELAY_MS = 15_000
export const LIVE_CONFIRM_DELAY_MS = 2_000
export const RELOAD_COOLDOWN_MS = 2 * 60_000
export const RELOAD_KEY = 'app_update_reload_at'

const ENTRY_PATTERN = /\/assets\/index-([A-Za-z0-9_-]+)\.js/

export interface BuildIdentity {
  /** 服务端版本号，例如 2.3.157。 */
  version: string
  /** 入口脚本哈希；读不到时为空串。 */
  entry: string
}

export interface AppUpdateDeps {
  bootVersion: string
  bootEntry: string
  /** 读服务端当前的版本与入口哈希；失败返回 null（离线/超时都不算「过期」）。 */
  fetchIdentity: (known: BuildIdentity) => Promise<BuildIdentity | null>
  now: () => number
  storage: Pick<Storage, 'getItem' | 'setItem'> | null
  /** 整页加载目标地址（替换当前页）。 */
  navigate: (url: string) => void
  reload: () => void
}

export interface AppUpdate {
  /** 已确认这个页面是旧版本。 */
  updateAvailable: Ref<boolean>
  latestVersion: Ref<string>
  /** 「页面已更新，必须刷新」对话框是否打开。 */
  blockingOpen: Ref<boolean>
  /** 安装探测：定时器、可见性/联网监听、路由跳转时整页加载。返回卸载函数。 */
  start: (router: Router) => () => void
  /** 立即做一次后台检查（含二次确认）。 */
  check: () => Promise<boolean>
  /** 敏感操作前调用：页面是最新的返回 true；已过期则打开对话框并返回 false。 */
  ensureFresh: () => Promise<boolean>
  /** 用户点「刷新」：总是允许，不受冷却限制。 */
  reloadNow: () => void
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))

/** 前端文件真的变了才算过期；读不到哈希时退回按版本号判断。 */
export function isStale(known: BuildIdentity, latest: BuildIdentity): boolean {
  if (!known.version || !latest.version) return false
  if (known.version === latest.version) return false
  if (known.entry && latest.entry) return known.entry !== latest.entry
  return true
}

export function readEntryHash(doc: Pick<Document, 'querySelector'>): string {
  const el = doc.querySelector('script[type="module"][src*="/assets/index-"]')
  return ENTRY_PATTERN.exec(el?.getAttribute('src') ?? '')?.[1] ?? ''
}

export function createAppUpdate(deps: AppUpdateDeps): AppUpdate {
  const updateAvailable = ref(false)
  const latestVersion = ref('')
  const blockingOpen = ref(false)
  let lastCheckAt = 0
  let running: Promise<boolean> | null = null

  const known = (): BuildIdentity => ({ version: deps.bootVersion, entry: deps.bootEntry })

  /** 单次观测：true=确认比当前页新；false=一样新或读不到。 */
  const observeStale = async (): Promise<boolean> => {
    const latest = await deps.fetchIdentity(known())
    lastCheckAt = deps.now()
    if (!latest) return false
    if (!isStale(known(), latest)) return false
    latestVersion.value = latest.version
    return true
  }

  const check = (): Promise<boolean> => {
    if (updateAvailable.value) return Promise.resolve(true)
    // 没有启动版本（开发环境 / 没注入配置）就没有可比较的对象。
    if (!deps.bootVersion) return Promise.resolve(false)
    if (running) return running
    running = (async () => {
      try {
        if (!(await observeStale())) return false
        await sleep(CONFIRM_DELAY_MS)
        if (!(await observeStale())) return false
        updateAvailable.value = true
        return true
      } finally {
        running = null
      }
    })()
    return running
  }

  const ensureFresh = async (): Promise<boolean> => {
    if (updateAvailable.value) {
      blockingOpen.value = true
      return false
    }
    if (!deps.bootVersion) return true
    if (!(await observeStale())) return true
    await sleep(LIVE_CONFIRM_DELAY_MS)
    if (!(await observeStale())) return true
    updateAvailable.value = true
    blockingOpen.value = true
    return false
  }

  /** 自动刷新（路由跳转触发）：2 分钟冷却，避免反复重载。 */
  const reloadForUpdate = (target: string): boolean => {
    const now = deps.now()
    const last = Number(deps.storage?.getItem(RELOAD_KEY) ?? 0)
    if (last > 0 && now - last < RELOAD_COOLDOWN_MS) return false
    deps.storage?.setItem(RELOAD_KEY, String(now))
    deps.navigate(target)
    return true
  }

  const reloadNow = () => {
    deps.storage?.setItem(RELOAD_KEY, String(deps.now()))
    deps.reload()
  }

  const start = (router: Router): (() => void) => {
    const removeGuard = router.beforeEach((to, from) => {
      if (!updateAvailable.value) return true
      if (to.fullPath === from.fullPath) return true
      // 整页加载会替换这次 SPA 跳转；冷却中则照常跳转。
      return reloadForUpdate(to.fullPath) ? false : true
    })

    const first = setTimeout(() => void check(), FIRST_CHECK_DELAY_MS)
    const interval = setInterval(() => void check(), POLL_INTERVAL_MS)
    const onVisible = () => {
      if (document.visibilityState !== 'visible') return
      if (deps.now() - lastCheckAt < FOCUS_RECHECK_MIN_MS) return
      void check()
    }
    const onOnline = () => void check()
    document.addEventListener('visibilitychange', onVisible)
    window.addEventListener('online', onOnline)

    return () => {
      removeGuard()
      clearTimeout(first)
      clearInterval(interval)
      document.removeEventListener('visibilitychange', onVisible)
      window.removeEventListener('online', onOnline)
    }
  }

  return { updateAvailable, latestVersion, blockingOpen, start, check, ensureFresh, reloadNow }
}

/** 读服务端当前的版本与入口哈希。任何失败都返回 null，绝不抛出。 */
async function fetchIdentityFromServer(known: BuildIdentity): Promise<BuildIdentity | null> {
  const bust = `_=${Date.now()}`
  try {
    const res = await fetch(`${buildApiUrl('/settings/public')}?${bust}`, {
      cache: 'no-store',
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    })
    if (!res.ok) return null
    const body = (await res.json()) as { data?: { version?: unknown }; version?: unknown }
    const version = String(body?.data?.version ?? body?.version ?? '')
    if (!version) return null
    if (version === known.version) return { version, entry: known.entry }

    // 版本号变了：再看入口脚本哈希，前端文件没变（纯后端发版）就不打扰用户。
    const page = await fetch(`/?${bust}`, {
      cache: 'no-store',
      credentials: 'same-origin',
      headers: { Accept: 'text/html' },
    })
    if (!page.ok) return { version, entry: '' }
    return { version, entry: ENTRY_PATTERN.exec(await page.text())?.[1] ?? '' }
  } catch {
    return null
  }
}

function safeSessionStorage(): AppUpdateDeps['storage'] {
  try {
    return typeof window === 'undefined' ? null : window.sessionStorage
  } catch {
    return null
  }
}

// 本模块被导入时（main.ts 刷新公共配置之前）记下「这个页面是哪个版本」。
const BOOT: BuildIdentity = {
  version: typeof window === 'undefined' ? '' : String(window.__APP_CONFIG__?.version ?? ''),
  entry: typeof document === 'undefined' ? '' : readEntryHash(document),
}

let singleton: AppUpdate | null = null

export function useAppUpdate(): AppUpdate {
  if (!singleton) {
    singleton = createAppUpdate({
      bootVersion: BOOT.version,
      bootEntry: BOOT.entry,
      fetchIdentity: fetchIdentityFromServer,
      now: () => Date.now(),
      storage: safeSessionStorage(),
      navigate: (url) => window.location.assign(url),
      reload: () => window.location.reload(),
    })
  }
  return singleton
}
