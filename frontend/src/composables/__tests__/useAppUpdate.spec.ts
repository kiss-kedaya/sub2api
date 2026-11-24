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
  compareVersions,
  createAppUpdate,
  fetchIdentityFromServer,
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

describe('isStale during a rolling release', () => {
  it('is not stale when the server is OLDER than the page (the balancer sent us to the machine not upgraded yet)', () => {
    // page loaded from the new machine, the poll lands on the old one
    expect(isStale(NEW, OLD)).toBe(false)
    expect(isStale(NEW, { version: OLD.version, entry: '' })).toBe(false)
  })

  it('compares numerically, not as text: 2.3.10 is newer than 2.3.9', () => {
    expect(isStale({ version: '2.3.9', entry: 'a' }, { version: '2.3.10', entry: 'b' })).toBe(true)
    expect(isStale({ version: '2.3.10', entry: 'b' }, { version: '2.3.9', entry: 'a' })).toBe(false)
  })

  it('falls back to "different means stale" when a version cannot be parsed', () => {
    expect(isStale({ version: 'dev', entry: 'a' }, { version: '2.3.158', entry: 'b' })).toBe(true)
    expect(isStale({ version: '2.3.158', entry: '' }, { version: 'nightly', entry: '' })).toBe(true)
  })
})

describe('compareVersions', () => {
  it('orders by number per segment', () => {
    expect(compareVersions('2.3.158', '2.3.157')).toBeGreaterThan(0)
    expect(compareVersions('2.3.157', '2.3.158')).toBeLessThan(0)
    expect(compareVersions('2.3.158', '2.3.158')).toBe(0)
    expect(compareVersions('2.10.0', '2.9.9')).toBeGreaterThan(0)
  })

  it('treats a missing segment as 0 and ignores a leading v and a suffix', () => {
    expect(compareVersions('2.3', '2.3.0')).toBe(0)
    expect(compareVersions('v2.3.158', '2.3.158')).toBe(0)
    expect(compareVersions('2.3.158-rc1', '2.3.158')).toBe(0)
    expect(compareVersions('2.4', '2.3.99')).toBeGreaterThan(0)
  })

  it('looks at every segment of the longer version', () => {
    expect(compareVersions('2.3.1', '2.3')).toBeGreaterThan(0)
    expect(compareVersions('2.3', '2.3.1')).toBeLessThan(0)
  })

  it('returns null when either side is not a version', () => {
    expect(compareVersions('', '2.3.158')).toBeNull()
    expect(compareVersions('2.3.158', 'dev')).toBeNull()
  })
})

describe('fetchIdentityFromServer', () => {
  const json = (body: unknown, ok = true) => ({ ok, json: async () => body, text: async () => '' })
  const html = (src: string, ok = true) => ({
    ok,
    json: async () => ({}),
    text: async () => `<html><script type="module" crossorigin src="${src}"></script></html>`,
  })
  const known: BuildIdentity = { version: '2.3.157', entry: 'AAAA1111' }
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('reads the version from /settings/public and skips the page when it is unchanged', async () => {
    fetchMock.mockResolvedValueOnce(json({ code: 0, data: { version: '2.3.157' } }))
    await expect(fetchIdentityFromServer(known)).resolves.toEqual(known)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(String(fetchMock.mock.calls[0][0])).toContain('/settings/public')
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ cache: 'no-store' })
  })

  it('does not read the page when the server is older than this page (rolling release)', async () => {
    fetchMock.mockResolvedValueOnce(json({ data: { version: '2.3.156' } }))
    await expect(fetchIdentityFromServer(known)).resolves.toEqual({ version: '2.3.156', entry: known.entry })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('reads the entry hash from the page when the server is newer', async () => {
    fetchMock
      .mockResolvedValueOnce(json({ data: { version: '2.3.158' } }))
      .mockResolvedValueOnce(html('/assets/index-BBBB2222.js'))
    await expect(fetchIdentityFromServer(known)).resolves.toEqual({ version: '2.3.158', entry: 'BBBB2222' })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(String(fetchMock.mock.calls[1][0])).toMatch(/^\/\?_=\d+$/)
  })

  it('keeps the new version with an empty hash when the page cannot be read', async () => {
    fetchMock
      .mockResolvedValueOnce(json({ data: { version: '2.3.158' } }))
      .mockResolvedValueOnce(html('/assets/index-BBBB2222.js', false))
    await expect(fetchIdentityFromServer(known)).resolves.toEqual({ version: '2.3.158', entry: '' })
  })

  it('answers null for a non-200 without reading anything else, even if the body looks newer', async () => {
    fetchMock.mockResolvedValueOnce(json({ data: { version: '2.3.158' } }, false))
    await expect(fetchIdentityFromServer(known)).resolves.toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('answers null for an empty version without reading the page', async () => {
    fetchMock.mockResolvedValueOnce(json({ data: { version: '' } }))
    await expect(fetchIdentityFromServer(known)).resolves.toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('answers null (not stale) on a network error', async () => {
    fetchMock.mockRejectedValueOnce(new Error('offline'))
    await expect(fetchIdentityFromServer(known)).resolves.toBeNull()
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
