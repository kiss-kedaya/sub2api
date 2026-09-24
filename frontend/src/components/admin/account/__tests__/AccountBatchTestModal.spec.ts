import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountBatchTestModal from '../AccountBatchTestModal.vue'

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

function createStreamResponse(model: string) {
  const encoder = new TextEncoder()
  const chunks = [
    encoder.encode(`data: {"type":"test_start","model":"${model}"}\n`),
    encoder.encode('data: {"type":"test_complete","success":true}\n')
  ]
  let index = 0
  return {
    ok: true,
    body: {
      getReader: () => ({
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
    vi.restoreAllMocks()
  })

  it('指定模型后会对每个账号发起测试请求', async () => {
    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-test="batch-test-scope-selected"]').trigger('click')
    await flushPromises()

    const modelBox = wrapper.get('[data-test="batch-test-model-gpt-5.4"]')
    const el = modelBox.element as HTMLInputElement
    el.checked = true
    await modelBox.trigger('change')
    await wrapper.get('[data-test="batch-test-start"]').trigger('click')
    await vi.waitFor(() => {
      expect(global.fetch).toHaveBeenCalledTimes(2)
    })

    const bodies = (global.fetch as any).mock.calls.map(([, request]: [string, { body: string }]) => JSON.parse(request.body))
    expect(bodies).toEqual([
      { model_id: 'gpt-5.4', prompt: '', mode: 'default' },
      { model_id: 'gpt-5.4', prompt: '', mode: 'default' }
    ])
  })

  it('默认全部模型会跳过媒体模型', async () => {
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-test="batch-test-start"]').trigger('click')
    await vi.waitFor(() => {
      expect(global.fetch).toHaveBeenCalledTimes(2)
    })

    const bodies = (global.fetch as any).mock.calls.map(([, request]: [string, { body: string }]) => JSON.parse(request.body))
    expect(bodies).toEqual([
      { model_id: 'gpt-5.4', prompt: '', mode: 'default' },
      { model_id: 'gpt-5.4', prompt: '', mode: 'default' }
    ])
    expect(bodies.some((body: { model_id: string }) => body.model_id === 'gpt-image-1')).toBe(false)
  })
})
