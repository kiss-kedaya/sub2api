import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Router } from 'vue-router'
import {
  CONFIRM_DELAY_MS,
  FIRST_CHECK_DELAY_MS,
  FOCUS_RECHECK_MIN_MS,
  LIVE_CONFIRM_DELAY_MS,
  POLL_INTERVAL_MS,
  RELOAD_COOLDOWN_MS,
  RELOAD_KEY,
  createAppUpdate,
  isStale,
  readEntryHash,
  type AppUpdateDeps,
  type BuildIdentity,
} from '../useAppUpdate'

const OLD: BuildIdentity = { version: '2.3.156', entry: 'AAAA1111' }
const NEW: BuildIdentity = { version: '2.3.157', entry: 'BBBB2222' }

function memoryStorage(initial: Record<string, string> = {}) {
  const data = { ...initial }
  return {
    getItem: (key: string) => (key in data ? data[key] : null),
    setItem: (key: string, value: string) => {
      data[key] = value
    },
    data,
  }
}

function setup(overrides: Partial<AppUpdateDeps> = {}, answers: Array<BuildIdentity | null> = [OLD]) {
  let clock = 1_000_000
  const queue = [...answers]
  const fetchIdentity = vi.fn(async () => (queue.length > 1 ? queue.shift()! : queue[0]))
  const storage = memoryStorage()
  const navigate = vi.fn()
  const reload = vi.fn()
  const deps: AppUpdateDeps = {
    bootVersion: OLD.version,
    bootEntry: OLD.entry,
    fetchIdentity,
    now: () => clock,
    storage,
    navigate,
    reload,
    ...overrides,
  }
  return {
    update: createAppUpdate(deps),
    fetchIdentity,
    storage,
    navigate,
    reload,
    advanceClock: (ms: number) => {
      clock += ms
    },
  }
}

function fakeRouter() {
  let guard: ((to: { fullPath: string }, from: { fullPath: string }) => unknown) | null = null
  const router = {
    beforeEach: (fn: typeof guard) => {
      guard = fn
      return () => {
        guard = null
      }
    },
  } as unknown as Router
  return {
    router,
    navigateTo: (to: string, from = '/dashboard') => guard!({ fullPath: to }, { fullPath: from }),
    hasGuard: () => guard !== null,
  }
}

describe('isStale', () => {
  it('is false when there is nothing to compare', () => {
    expect(isStale({ version: '', entry: '' }, NEW)).toBe(false)
    expect(isStale(OLD, { version: '', entry: '' })).toBe(false)
  })

  it('is false for the same version', () => {
    expect(isStale(OLD, { ...OLD })).toBe(false)
  })

  it('trusts the version number first: the same version is never stale, even if the entry hash differs', () => {
    // e.g. a rebuild of the same version string, or an entry hash read from a different edge node
    expect(isStale(OLD, { version: OLD.version, entry: 'CCCC3333' })).toBe(false)
    expect(isStale(OLD, { version: OLD.version, entry: '' })).toBe(false)
  })

  it('ignores a backend-only release: new version but the same frontend entry', () => {
    expect(isStale(OLD, { version: '2.3.157', entry: OLD.entry })).toBe(false)
  })

  it('is true when the frontend entry really changed', () => {
    expect(isStale(OLD, NEW)).toBe(true)
  })

  it('falls back to the version number when an entry hash is unavailable', () => {
    expect(isStale(OLD, { version: '2.3.157', entry: '' })).toBe(true)
    expect(isStale({ version: '2.3.156', entry: '' }, NEW)).toBe(true)
  })
})

describe('readEntryHash', () => {
  const docWith = (src: string | null) => ({
    querySelector: () => (src === null ? null : { getAttribute: () => src }),
  })

  it('reads the hash of the module entry script', () => {
    expect(readEntryHash(docWith('/assets/index-CkTYfw0n.js'))).toBe('CkTYfw0n')
    expect(readEntryHash(docWith('https://cdn.example.com/assets/index-a_b-9.js'))).toBe('a_b-9')
  })

  it('returns an empty string when there is no entry script (dev / tests)', () => {
    expect(readEntryHash(docWith(null))).toBe('')
    expect(readEntryHash(docWith('/src/main.ts'))).toBe('')
  })
})

describe('createAppUpdate', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  describe('check', () => {
    it('does nothing without a boot version (nothing to compare against)', async () => {
      const { update, fetchIdentity } = setup({ bootVersion: '', bootEntry: '' })
      await expect(update.check()).resolves.toBe(false)
      expect(fetchIdentity).not.toHaveBeenCalled()
    })

    it('stays quiet when the server still has the same version', async () => {
      const { update } = setup({}, [OLD])
      await expect(update.check()).resolves.toBe(false)
      expect(update.updateAvailable.value).toBe(false)
    })

    it('treats an unreachable server as not stale', async () => {
      const { update } = setup({}, [null])
      await expect(update.check()).resolves.toBe(false)
      expect(update.updateAvailable.value).toBe(false)
    })

    it('does not flag a single stale observation (canary flapping): needs a second one', async () => {
      const { update, fetchIdentity } = setup({}, [NEW, OLD])
      const run = update.check()
      await vi.advanceTimersByTimeAsync(CONFIRM_DELAY_MS)
      await expect(run).resolves.toBe(false)
      expect(fetchIdentity).toHaveBeenCalledTimes(2)
      expect(update.updateAvailable.value).toBe(false)
    })

    it('flags the page once the stale result is confirmed, and remembers the new version', async () => {
      const { update, fetchIdentity } = setup({}, [NEW])
      const run = update.check()
      await vi.advanceTimersByTimeAsync(CONFIRM_DELAY_MS)
      await expect(run).resolves.toBe(true)
      expect(update.updateAvailable.value).toBe(true)
      expect(update.latestVersion.value).toBe('2.3.157')

      // Sticky: no more network once flagged.
      fetchIdentity.mockClear()
      await expect(update.check()).resolves.toBe(true)
      expect(fetchIdentity).not.toHaveBeenCalled()
    })

    it('runs one check at a time', async () => {
      const { update, fetchIdentity } = setup({}, [NEW])
      const first = update.check()
      const second = update.check()
      await vi.advanceTimersByTimeAsync(CONFIRM_DELAY_MS)
      await Promise.all([first, second])
      expect(fetchIdentity).toHaveBeenCalledTimes(2)
    })
  })

  describe('ensureFresh', () => {
    it('lets the action through when the page is current', async () => {
      const { update } = setup({}, [OLD])
      await expect(update.ensureFresh()).resolves.toBe(true)
      expect(update.blockingOpen.value).toBe(false)
    })

    it('lets the action through when the server cannot be reached', async () => {
      const { update } = setup({}, [null])
      await expect(update.ensureFresh()).resolves.toBe(true)
      expect(update.blockingOpen.value).toBe(false)
    })

    it('lets the action through without a boot version', async () => {
      const { update, fetchIdentity } = setup({ bootVersion: '', bootEntry: '' })
      await expect(update.ensureFresh()).resolves.toBe(true)
      expect(fetchIdentity).not.toHaveBeenCalled()
    })

    it('blocks and opens the dialog when the page is confirmed stale', async () => {
      const { update } = setup({}, [NEW])
      const run = update.ensureFresh()
      await vi.advanceTimersByTimeAsync(LIVE_CONFIRM_DELAY_MS)
      await expect(run).resolves.toBe(false)
      expect(update.updateAvailable.value).toBe(true)
      expect(update.blockingOpen.value).toBe(true)
    })

    it('does not block on a one-off stale answer', async () => {
      const { update } = setup({}, [NEW, OLD])
      const run = update.ensureFresh()
      await vi.advanceTimersByTimeAsync(LIVE_CONFIRM_DELAY_MS)
      await expect(run).resolves.toBe(true)
      expect(update.blockingOpen.value).toBe(false)
      expect(update.updateAvailable.value).toBe(false)
    })

    it('blocks immediately, without touching the network, once the page is already flagged', async () => {
      const { update, fetchIdentity } = setup({}, [NEW])
      const run = update.check()
      await vi.advanceTimersByTimeAsync(CONFIRM_DELAY_MS)
      await run
      fetchIdentity.mockClear()

      await expect(update.ensureFresh()).resolves.toBe(false)
      expect(update.blockingOpen.value).toBe(true)
      expect(fetchIdentity).not.toHaveBeenCalled()
    })
  })

  describe('reloadNow', () => {
    it('always reloads (user clicked) and records the time', () => {
      const { update, reload, storage } = setup()
      storage.setItem(RELOAD_KEY, String(1_000_000)) // inside the cooldown
      update.reloadNow()
      expect(reload).toHaveBeenCalledTimes(1)
      expect(Number(storage.getItem(RELOAD_KEY))).toBe(1_000_000)
    })
  })

  describe('start', () => {
    async function flagged(deps: Partial<AppUpdateDeps> = {}) {
      const ctx = setup(deps, [NEW])
      const run = ctx.update.check()
      await vi.advanceTimersByTimeAsync(CONFIRM_DELAY_MS)
      await run
      return ctx
    }

    it('lets navigation through while the page is current', () => {
      const { update, navigate } = setup()
      const { router, navigateTo } = fakeRouter()
      update.start(router)
      expect(navigateTo('/keys')).toBe(true)
      expect(navigate).not.toHaveBeenCalled()
    })

    it('turns the next navigation into a full page load once the page is stale', async () => {
      const { update, navigate, storage } = await flagged()
      const { router, navigateTo } = fakeRouter()
      update.start(router)

      expect(navigateTo('/keys?tab=usage')).toBe(false)
      expect(navigate).toHaveBeenCalledWith('/keys?tab=usage')
      expect(storage.getItem(RELOAD_KEY)).not.toBeNull()
    })

    it('does not reload when the target is the current page', async () => {
      const { update, navigate } = await flagged()
      const { router, navigateTo } = fakeRouter()
      update.start(router)
      expect(navigateTo('/dashboard', '/dashboard')).toBe(true)
      expect(navigate).not.toHaveBeenCalled()
    })

    it('does not reload again inside the cooldown, so a flapping server cannot loop the page', async () => {
      const { update, navigate, advanceClock } = await flagged()
      const { router, navigateTo } = fakeRouter()
      update.start(router)

      expect(navigateTo('/keys')).toBe(false)
      expect(navigate).toHaveBeenCalledTimes(1)

      advanceClock(RELOAD_COOLDOWN_MS - 1)
      expect(navigateTo('/usage')).toBe(true) // ordinary SPA navigation
      expect(navigate).toHaveBeenCalledTimes(1)

      advanceClock(2)
      expect(navigateTo('/usage')).toBe(false)
      expect(navigate).toHaveBeenCalledTimes(2)
    })

    it('checks once shortly after start, then on a timer', async () => {
      const { update, fetchIdentity } = setup({}, [OLD])
      const { router } = fakeRouter()
      update.start(router)

      expect(fetchIdentity).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(FIRST_CHECK_DELAY_MS)
      expect(fetchIdentity).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS)
      expect(fetchIdentity.mock.calls.length).toBeGreaterThanOrEqual(2)
    })

    it('rechecks when the tab becomes visible, but not more often than every 30s', async () => {
      const { update, fetchIdentity, advanceClock } = setup({}, [OLD])
      const { router } = fakeRouter()
      update.start(router)
      await update.check() // lastCheckAt = now
      fetchIdentity.mockClear()

      Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
      document.dispatchEvent(new Event('visibilitychange'))
      await vi.advanceTimersByTimeAsync(0)
      expect(fetchIdentity).not.toHaveBeenCalled()

      advanceClock(FOCUS_RECHECK_MIN_MS + 1)
      document.dispatchEvent(new Event('visibilitychange'))
      await vi.advanceTimersByTimeAsync(0)
      expect(fetchIdentity).toHaveBeenCalledTimes(1)
    })

    it('removes its guard and listeners when stopped', async () => {
      const { update, fetchIdentity } = setup({}, [OLD])
      const { router, hasGuard } = fakeRouter()
      const stop = update.start(router)
      expect(hasGuard()).toBe(true)

      stop()
      expect(hasGuard()).toBe(false)
      await vi.advanceTimersByTimeAsync(FIRST_CHECK_DELAY_MS + POLL_INTERVAL_MS)
      expect(fetchIdentity).not.toHaveBeenCalled()
    })
  })
})
