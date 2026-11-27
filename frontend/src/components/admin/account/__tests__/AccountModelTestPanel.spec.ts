import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountModelTestPanel from '../AccountModelTestPanel.vue'
import type { AccountModelTestEvent, BatchTestAccount } from '@/utils/accountModelTest'

enableAutoUnmount(afterEach)

const { getAvailableModels } = vi.hoisted(() => ({ getAvailableModels: vi.fn() }))
const { routerPush } = vi.hoisted(() => ({ routerPush: vi.fn() }))

vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getAvailableModels } } }))
vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return { ...actual, useRouter: () => ({ push: routerPush }) }
})
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

function deferred<Value>() {
  let resolve!: (value: Value) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<Value>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

function createAbortIgnoringStream(initial: AccountModelTestEvent[] = []) {
  const pending = deferred<ReadableStreamReadResult<Uint8Array>>()
  const encode = (events: AccountModelTestEvent[]) => new TextEncoder().encode(
    events.map((event) => 'data: ' + JSON.stringify(event) + '\n\n').join('')
  )
  const read = vi.fn()
  if (initial.length) read.mockResolvedValueOnce({ done: false, value: encode(initial) })
  read.mockImplementationOnce(() => pending.promise).mockResolvedValue({ done: true })
  const reader = { read, cancel: vi.fn().mockResolvedValue(undefined), releaseLock: vi.fn() }
  return {
    reader,
    response: { ok: true, body: { getReader: () => reader } } as unknown as Response,
    finish: (events: AccountModelTestEvent[]) => pending.resolve({ done: false, value: encode(events) }),
    fail: (error: Error) => pending.reject(error)
  }
}

function createCompletedStream(events: AccountModelTestEvent[]) {
  const encoded = new TextEncoder().encode(events.map((event) => 'data: ' + JSON.stringify(event) + '\n\n').join(''))
  const read = vi.fn().mockResolvedValueOnce({ done: false, value: encoded }).mockResolvedValue({ done: true })
  const reader = { read, cancel: vi.fn().mockResolvedValue(undefined), releaseLock: vi.fn() }
  return { response: { ok: true, body: { getReader: () => reader } } as unknown as Response, reader }
}

function createPendingFetch() {
  const signals: AbortSignal[] = []
  const releases: Array<() => void> = []
  const fetchMock = vi.fn((_url: string, init: RequestInit) => {
    if (init.signal) signals.push(init.signal)
    return new Promise<Response>((resolve) => {
      releases.push(() => resolve(createCompletedStream([{ type: 'test_complete', success: true }]).response))
    })
  })
  return { fetchMock, signals, releases }
}

const firstAccount = { id: 11, name: 'OpenAI A', platform: 'openai', type: 'apikey' }
const secondAccount = { id: 12, name: 'OpenAI B', platform: 'openai', type: 'apikey' }
const model = { id: 'gpt-one', display_name: 'GPT One' }

function mountPanel(accounts: BatchTestAccount[] = [firstAccount]) {
  return mount(AccountModelTestPanel, { props: { show: true, accounts } })
}

describe('AccountModelTestPanel race regressions', () => {
  beforeEach(() => {
    getAvailableModels.mockReset().mockResolvedValue([model])
    routerPush.mockReset()
    vi.stubGlobal('localStorage', { getItem: () => 'test-token' })
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('单账号工具栏不重复账号名和并发说明，未开始时不显示零统计', async () => {
    const wrapper = mount(AccountModelTestPanel, { props: { show: true, accounts: [firstAccount], variant: 'single' } })
    await flushPromises()
    expect(wrapper.get('.model-test-kicker').text()).toBe('admin.accounts.modelCount')
    expect(wrapper.get('.model-test-toolbar').text()).not.toContain(firstAccount.name)
    expect(wrapper.text()).not.toContain('unboundedConcurrency')
    expect(wrapper.find('.model-test-progress').exists()).toBe(false)
    expect(wrapper.get('input[type="search"]').attributes('aria-label')).toBe('admin.accounts.batchTest.filterModels')
  })

  it('失败筛选按钮暴露选中状态，按钮保持在同一操作组', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    const filter = wrapper.get('.model-test-filter')
    expect(filter.attributes('aria-pressed')).toBe('false')
    await filter.trigger('click')
    expect(filter.attributes('aria-pressed')).toBe('true')
    expect(wrapper.findAll('tbody tr')).toHaveLength(0)
    await filter.trigger('click')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.findAll('.model-test-selection button')).toHaveLength(2)
  })

  it.each([
    ['all', 'success'],
    ['all', 'error'],
    ['one', 'success'],
    ['one', 'error']
  ] as const)('取消 %s 后立即重跑，旧 %s 不覆盖新结果或释放新控制器', async (scope, outcome) => {
    const oldStream = createAbortIgnoringStream([{ type: 'content', text: 'old partial' }])
    const newStream = createAbortIgnoringStream()
    vi.mocked(fetch).mockResolvedValueOnce(oldStream.response).mockResolvedValueOnce(newStream.response)
    const wrapper = mountPanel()
    await flushPromises()
    const startSelector = scope === 'all' ? '[data-test="test-all-models"]' : '[data-test="row-test-11-gpt-one"]'
    await wrapper.get(startSelector).trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="test-output-11-gpt-one"]').text()).toBe('old partial')
    const firstSignal = vi.mocked(fetch).mock.calls[0][1]?.signal

    await wrapper.get('[data-test="test-stop"]').trigger('click')
    expect(firstSignal?.aborted).toBe(true)
    expect(oldStream.reader.cancel).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-test="test-output-11-gpt-one"]').text()).toBe('-')
    await wrapper.get(startSelector).trigger('click')
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(2)
    const secondSignal = vi.mocked(fetch).mock.calls[1][1]?.signal

    if (outcome === 'success') {
      oldStream.finish([{ type: 'content', text: 'stale answer' }, { type: 'test_complete', success: true }])
    } else {
      oldStream.fail(new Error('stale transport failure'))
    }
    await flushPromises()
    expect(oldStream.reader.releaseLock).toHaveBeenCalledOnce()
    expect(secondSignal?.aborted).toBe(false)
    expect(wrapper.find('[data-test="test-stop"]').exists()).toBe(true)
    expect(wrapper.findAll('tbody tr.is-running')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('old partial')
    expect(wrapper.text()).not.toContain('stale')

    newStream.finish([{ type: 'content', text: 'fresh answer' }, { type: 'test_complete', success: true }])
    await flushPromises()
    expect(wrapper.get('[data-test="test-output-11-gpt-one"]').text()).toBe('fresh answer')
    expect(wrapper.findAll('tbody tr.is-success')).toHaveLength(1)
    expect(wrapper.get('tbody tr').findAll('td')[3].text()).toMatch(/^\d+(?:\.\d+)?(?:ms|s)$/)
    expect(wrapper.find('[data-test="test-stop"]').exists()).toBe(false)
  })

  it('旧请求最后结束也不能覆盖已完成的新结果和耗时', async () => {
    const oldStream = createAbortIgnoringStream()
    const newStream = createAbortIgnoringStream()
    vi.mocked(fetch).mockResolvedValueOnce(oldStream.response).mockResolvedValueOnce(newStream.response)
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="test-stop"]').trigger('click')
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    newStream.finish([{ type: 'content', text: 'new completed' }, { type: 'test_complete', success: true }])
    await flushPromises()
    const completedRow = wrapper.get('tbody tr').text()
    oldStream.finish([{ type: 'error', error: 'old rejected' }])
    await flushPromises()
    expect(wrapper.get('tbody tr').text()).toBe(completedRow)
    expect(wrapper.findAll('tbody tr.is-success')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('old rejected')
  })

  it('旧轮收尾后再次停止仍能取消新轮所有请求', async () => {
    const oldStream = createAbortIgnoringStream()
    const newStream = createAbortIgnoringStream()
    vi.mocked(fetch).mockResolvedValueOnce(oldStream.response).mockResolvedValueOnce(newStream.response)
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="test-stop"]').trigger('click')
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    oldStream.fail(new Error('old request aborted late'))
    await flushPromises()
    await wrapper.get('[data-test="test-stop"]').trigger('click')
    expect(vi.mocked(fetch).mock.calls[1][1]?.signal?.aborted).toBe(true)
    expect(newStream.reader.cancel).toHaveBeenCalledOnce()
    newStream.finish([{ type: 'test_complete', success: true }])
    await flushPromises()
    expect(wrapper.findAll('tbody tr.is-idle')).toHaveLength(1)
    expect(wrapper.find('[data-test="test-stop"]').exists()).toBe(false)
  })

  it.each(['success', 'error'] as const)('账号 A → B → A 时隔离旧目录 %s，未加载新目录前不放行测试', async (outcome) => {
    const oldCatalog = deferred<Array<typeof model>>()
    const otherCatalog = deferred<Array<typeof model>>()
    const newCatalog = deferred<Array<typeof model>>()
    getAvailableModels.mockReset()
      .mockReturnValueOnce(oldCatalog.promise)
      .mockReturnValueOnce(otherCatalog.promise)
      .mockReturnValueOnce(newCatalog.promise)
    const wrapper = mountPanel()
    await wrapper.setProps({ accounts: [secondAccount] })
    await wrapper.setProps({ accounts: [firstAccount] })
    expect(getAvailableModels.mock.calls.map(([accountId]) => accountId)).toEqual([11, 12, 11])
    if (outcome === 'success') oldCatalog.resolve([{ id: 'gpt-stale', display_name: 'Stale' }])
    else oldCatalog.reject(new Error('old catalog error'))
    otherCatalog.resolve([{ id: 'gpt-other', display_name: 'Other' }])
    await flushPromises()
    expect(wrapper.findAll('tbody tr')).toHaveLength(0)
    expect(wrapper.get<HTMLButtonElement>('[data-test="test-all-models"]').element.disabled).toBe(true)
    expect(wrapper.text()).not.toContain('old catalog error')
    expect(fetch).not.toHaveBeenCalled()

    newCatalog.resolve([{ id: 'gpt-fresh', display_name: 'Fresh' }])
    await flushPromises()
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.text()).toContain('gpt-fresh')
    expect(wrapper.text()).not.toContain('gpt-stale')
    expect(wrapper.text()).not.toContain('gpt-other')
    const stream = createAbortIgnoringStream()
    vi.mocked(fetch).mockResolvedValueOnce(stream.response)
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(vi.mocked(fetch).mock.calls[0][0]).toContain('/admin/accounts/11/test')
    expect(JSON.parse(String(vi.mocked(fetch).mock.calls[0][1]?.body)).model_id).toBe('gpt-fresh')
    stream.finish([{ type: 'test_complete', success: true }])
    await flushPromises()
  })

  it('关闭再打开时旧目录不能覆盖新目录缓存', async () => {
    const oldCatalog = deferred<Array<typeof model>>()
    const newCatalog = deferred<Array<typeof model>>()
    getAvailableModels.mockReset().mockReturnValueOnce(oldCatalog.promise).mockReturnValueOnce(newCatalog.promise)
    const wrapper = mountPanel()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    newCatalog.resolve([model])
    await flushPromises()
    oldCatalog.resolve([{ id: 'gpt-stale', display_name: 'Stale' }])
    await flushPromises()
    await wrapper.get('.model-test-option [role="checkbox"]').trigger('click')
    expect(wrapper.find('[data-test="row-test-11-gpt-one"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('gpt-stale')
    expect(fetch).not.toHaveBeenCalled()
  })

  it('整体进度显示 n/m 与百分比，成功后计数正确', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(
      createCompletedStream([
        { type: 'content', text: 'ok' },
        { type: 'test_complete', success: true }
      ]).response
    )
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="test-progress"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="test-progress-count"]').text()).toBe('1 / 1 · 100%')
    expect(wrapper.findAll('tbody tr.is-success')).toHaveLength(1)
  })

  it('批量运行时可单独取消某一行，不影响同批其它模型', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-a', display_name: 'A' },
      { id: 'gpt-b', display_name: 'B' }
    ])
    const pending = createPendingFetch()
    vi.stubGlobal('fetch', pending.fetchMock)
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('tbody tr.is-running')).toHaveLength(2)

    await wrapper.get('[data-test="cancel-row-11-gpt-a"]').trigger('click')
    expect(pending.signals[0]?.aborted).toBe(true)
    expect(pending.signals[1]?.aborted).toBe(false)
    expect(wrapper.findAll('tbody tr.is-running')).toHaveLength(1)
    expect(wrapper.find('[data-test="cancel-row-11-gpt-b"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="row-test-11-gpt-a"]').exists()).toBe(true)
  })

  it('上游 401 归为 Key/权限失败并给出账号编辑深链', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      json: async () => ({ message: 'invalid api key' })
    }))
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    const failure = wrapper.get('[data-test="test-failure-11-gpt-one"]')
    expect(failure.classes()).toContain('is-auth')
    expect(failure.text()).toContain('admin.accounts.batchTest.failure.auth.label')
    await wrapper.get('[data-test="test-failure-fix-11-gpt-one"]').trigger('click')
    expect(routerPush).toHaveBeenCalledWith({ path: '/admin/accounts', query: { account: '11' } })
  })

  it.each([
    ['429', { type: 'error', error: 'HTTP 429: too many requests' }, 'rate_limit'],
    ['timeout', { type: 'error', error: 'upstream timeout while waiting for headers' }, 'network'],
    ['5xx', { type: 'error', error: 'HTTP 500: upstream exploded' }, 'other']
  ] as const)('%s 失败归类为 %s', async (_label, event, kind) => {
    vi.mocked(fetch).mockResolvedValueOnce(createCompletedStream([event]).response)
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    const failure = wrapper.get('[data-test="test-failure-11-gpt-one"]')
    expect(failure.classes()).toContain(`is-${kind}`)
  })

  it('uses GPU-composited progress motion, dvh sizing, and hover-gated styles', async () => {
    const { readFileSync } = await import('node:fs')
    const { resolve } = await import('node:path')
    const sfc = readFileSync(resolve(__dirname, '../AccountModelTestPanel.vue'), 'utf8')
    expect(sfc).toContain('@media (hover: hover) and (pointer: fine)')
    expect(sfc).toContain('prefers-reduced-motion: reduce')
    expect(sfc).toContain('scaleX(')
    expect(sfc).toContain('dvh')
    expect(sfc).not.toMatch(/transition[^;]*box-shadow/)
    expect(sfc).not.toMatch(/\dvh\b/)
  })
})
