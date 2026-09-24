import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountBatchTestModal from '../AccountBatchTestModal.vue'

enableAutoUnmount(afterEach)

const { getAvailableModels } = vi.hoisted(() => ({
  getAvailableModels: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAvailableModels
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (!params) return key
        return `${key}:${JSON.stringify(params)}`
      }
    })
  }
})

function createStreamResponse(model: string, text = `hello ${model}`) {
  const encoder = new TextEncoder()
  const chunks = [
    encoder.encode(`data: {"type":"test_start","model":"${model}"}\n`),
    encoder.encode(`data: {"type":"content","text":"${text}"}\n`),
    encoder.encode('data: {"type":"test_complete","success":true}\n')
  ]
  let index = 0
  return {
    ok: true,
    body: {
      getReader: () => ({
            cancel: vi.fn().mockResolvedValue(undefined),
            releaseLock: vi.fn(),
        read: vi.fn().mockImplementation(async () => {
          if (index < chunks.length) {
            return { done: false, value: chunks[index++] }
          }
          return { done: true, value: undefined }
        })
      })
    }
  } as Response
}

function mountModal() {
  return mount(AccountBatchTestModal, {
    props: {
      show: true,
      accounts: [
        { id: 11, name: 'OpenAI A', platform: 'openai', type: 'apikey' },
        { id: 12, name: 'OpenAI B', platform: 'openai', type: 'apikey' }
      ]
    },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
      }
    }
  })
}

describe('AccountBatchTestModal', () => {
  beforeEach(() => {
    getAvailableModels.mockReset()
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' },
      { id: 'gpt-image-1', display_name: 'GPT Image' }
    ])
    Object.defineProperty(globalThis, 'localStorage', {
      value: { getItem: () => 'test-token' },
      configurable: true
    })
    global.fetch = vi.fn().mockImplementation((_url: string, request: { body: string }) => {
      const model = JSON.parse(request.body).model_id
      return Promise.resolve(createStreamResponse(model))
    }) as any
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('测试全部会跳过媒体模型', async () => {
    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await vi.waitFor(() => {
      expect(global.fetch).toHaveBeenCalledTimes(2)
    })

    const bodies = (global.fetch as any).mock.calls.map(([, request]: [string, { body: string }]) => JSON.parse(request.body))
    expect(bodies).toEqual([
      { model_id: 'gpt-5.4', prompt: '', mode: 'default' },
      { model_id: 'gpt-5.4', prompt: '', mode: 'default' }
    ])
    await vi.waitFor(() => {
      expect(wrapper.get('[data-test="test-output-11-gpt-5.4"]').text()).toContain('hello gpt-5.4')
    })
  })

  it('多账号多模型会同时发出全部请求并展示返回', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' },
      { id: 'gpt-5.3', display_name: 'GPT-5.3' }
    ])

    let inflight = 0
    let peak = 0
    const releases: Array<() => void> = []
    global.fetch = vi.fn().mockImplementation((_url: string, request: { body: string }) => {
      const model = JSON.parse(request.body).model_id
      inflight += 1
      peak = Math.max(peak, inflight)
      return new Promise((resolve) => {
        releases.push(() => {
          inflight -= 1
          resolve(createStreamResponse(model))
        })
      })
    }) as any

    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await vi.waitFor(() => {
      expect(global.fetch).toHaveBeenCalledTimes(4)
    })
    expect(peak).toBe(4)

    releases.forEach((release) => release())
    await flushPromises()
    await vi.waitFor(() => {
      expect(wrapper.get('[data-test="test-output-11-gpt-5.4"]').text()).toContain('hello gpt-5.4')
      expect(wrapper.get('[data-test="test-output-11-gpt-5.3"]').text()).toContain('hello gpt-5.3')
    })
  })

  it('测试选中只打勾上的模型', async () => {
    const wrapper = mountModal()
    await flushPromises()

    const box = wrapper.get('[data-test="test-row-select-11-gpt-5.4"]')
    const el = box.element as HTMLInputElement
    el.checked = true
    await box.trigger('change')
    await wrapper.get('[data-test="test-selected-models"]').trigger('click')
    await vi.waitFor(() => {
      expect(global.fetch).toHaveBeenCalledTimes(1)
    })
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({ model_id: 'gpt-5.4', prompt: '', mode: 'default' })
  })

  it('搜索只显示一行时测全仍同时发出全部账号全部模型', async () => {
    const models = Array.from({ length: 8 }, (_, index) => ({ id: 'gpt-' + index, display_name: 'GPT ' + index }))
    getAvailableModels.mockResolvedValue(models)
    const releases: Array<() => void> = []
    const fetchMock = vi.fn((_url: string, request: RequestInit) => new Promise<Response>((resolve) => {
      const modelId = JSON.parse(String(request.body)).model_id
      releases.push(() => resolve(createStreamResponse(modelId)))
    }))
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('input[type="search"]').setValue('gpt-7')
    expect(wrapper.findAll('tbody tr')).toHaveLength(2)
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(16)
    const actualJobs = fetchMock.mock.calls.map(([url, request]) => ({
      accountId: Number(url.match(/accounts\/(\d+)\/test/)?.[1]),
      modelId: JSON.parse(String(request.body)).model_id
    }))
    expect(actualJobs).toEqual([11, 12].flatMap((accountId) => models.map(({ id }) => ({ accountId, modelId: id }))))
    releases.forEach((release) => release())
    await flushPromises()
    await wrapper.get('input[type="search"]').setValue('')
    expect(wrapper.findAll('tbody tr.is-success')).toHaveLength(16)
  })

  it('搜索隐藏已勾选模型后测选中不漏项也不扩大范围', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-alpha', display_name: 'Alpha' },
      { id: 'gpt-beta', display_name: 'Beta' }
    ])
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-test="test-row-select-11-gpt-alpha"]').setValue(true)
    await wrapper.get('[data-test="test-row-select-12-gpt-beta"]').setValue(true)
    await wrapper.get('input[type="search"]').setValue('gpt-beta')
    expect(wrapper.find('[data-test="test-row-select-11-gpt-alpha"]').exists()).toBe(false)
    await wrapper.get('[data-test="test-selected-models"]').trigger('click')
    await flushPromises()
    const calls = vi.mocked(fetch).mock.calls
    expect(calls).toHaveLength(2)
    expect(calls.map(([url, request]) => [String(url).match(/accounts\/(\d+)\/test/)?.[1], JSON.parse(String(request?.body)).model_id])).toEqual([
      ['11', 'gpt-alpha'], ['12', 'gpt-beta']
    ])
    await wrapper.get('input[type="search"]').setValue('')
    expect(wrapper.findAll('tbody tr.is-success')).toHaveLength(2)
    expect(wrapper.findAll('tbody tr.is-idle')).toHaveLength(2)
  })

  it('仅失败筛选下测全仍重测全部行', async () => {
    const fetchMock = vi.fn((_url: string, request: RequestInit) => Promise.resolve(createStreamResponse(JSON.parse(String(request.body)).model_id)))
    fetchMock.mockRejectedValueOnce(new Error('first attempt failed'))
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('tbody tr.is-failed')).toHaveLength(1)
    const failedOnly = wrapper.findAll('button').find((button) => button.text() === 'admin.accounts.batchTest.onlyFailed')
    expect(failedOnly).toBeDefined()
    await failedOnly!.trigger('click')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(4)
    expect(wrapper.findAll('tbody tr.is-success')).toHaveLength(2)
  })

  it('点关闭立即取消全部账号请求，不依赖父组件先隐藏弹窗', async () => {
    const releases: Array<() => void> = []
    const fetchMock = vi.fn((_url: string, request: RequestInit) => new Promise<Response>((resolve) => {
      releases.push(() => resolve(createStreamResponse(JSON.parse(String(request.body)).model_id)))
    }))
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-test="test-all-models"]').trigger('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await wrapper.get('.batch-test-footer button').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(fetchMock.mock.calls.every(([, request]) => request.signal?.aborted)).toBe(true)
    expect(wrapper.findAll('tbody tr.is-idle')).toHaveLength(2)
    releases.forEach((release) => release())
    await flushPromises()
    expect(wrapper.findAll('tbody tr.is-success')).toHaveLength(0)
    expect(wrapper.find('[data-test="test-stop"]').exists()).toBe(false)
  })
})
