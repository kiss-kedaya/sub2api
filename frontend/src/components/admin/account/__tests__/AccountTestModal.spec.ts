import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountTestModal from '../AccountTestModal.vue'
import type { AccountModelTestEvent } from '@/utils/accountModelTest'

enableAutoUnmount(afterEach)

const { getAvailableModels, copyToClipboard } = vi.hoisted(() => ({
  getAvailableModels: vi.fn(),
  copyToClipboard: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAvailableModels
    }
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const messages: Record<string, string> = {
    'admin.accounts.imagePromptDefault': 'Generate a cute orange cat astronaut sticker on a clean pastel background.'
  }
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'admin.accounts.imageReceived' && params?.count) {
          return `received-${params.count}`
        }
        if (key === 'admin.accounts.imagePreviewAlt' && params?.index) {
          return `test-image-${params.index}`
        }
        return messages[key] || key
      }
    })
  }
})

function createStreamResponse(lines: string[]) {
  const encoder = new TextEncoder()
  const chunks = lines.map((line) => encoder.encode(line))
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

function createAbortIgnoringStream(initial: AccountModelTestEvent[] = []) {
  let resolve!: (value: ReadableStreamReadResult<Uint8Array>) => void
  let reject!: (error: Error) => void
  const pending = new Promise<ReadableStreamReadResult<Uint8Array>>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  const encode = (events: AccountModelTestEvent[]) => new TextEncoder().encode(
    events.map((event) => 'data: ' + JSON.stringify(event) + '\n\n').join('')
  )
  const read = vi.fn()
  if (initial.length) read.mockResolvedValueOnce({ done: false, value: encode(initial) })
  read.mockImplementationOnce(() => pending).mockResolvedValue({ done: true })
  const reader = { read, cancel: vi.fn().mockResolvedValue(undefined), releaseLock: vi.fn() }
  return {
    reader,
    response: { ok: true, body: { getReader: () => reader } } as unknown as Response,
    finish: (events: AccountModelTestEvent[]) => resolve({ done: false, value: encode(events) }),
    fail: (error: Error) => reject(error)
  }
}

const grokAccount = { id: 13, name: 'Grok A', platform: 'grok', type: 'oauth', status: 'active' }
const oldAudio = 'data:audio/wav;base64,T0xE'
const newAudio = 'data:audio/wav;base64,TkVX'

function mountModal(account: Record<string, unknown> = {
  id: 42,
  name: 'Gemini Image Test',
  platform: 'gemini',
  type: 'apikey',
  status: 'active'
}) {
  return mount(AccountTestModal, {
    props: {
      show: false,
      account
    } as any,
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Select: {
          props: ['modelValue', 'options', 'disabled'],
          emits: ['update:modelValue'],
          template: '<select class="select-stub" :value="modelValue" :disabled="disabled" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>'
        },
        TextArea: {
          props: ['modelValue'],
          emits: ['update:modelValue'],
          template: '<textarea class="textarea-stub" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />'
        },
        Icon: true
      }
    }
  })
}

describe('AccountTestModal', () => {
  it('测试设置默认折叠，展开后保留填写内容', async () => {
    const wrapper = mountModal()
    const toggle = wrapper.get('.account-test-settings-toggle')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(wrapper.get('.account-test-settings-content').attributes('inert')).toBeDefined()
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('.account-test-settings-content').attributes('inert')).toBeUndefined()
    await wrapper.get('textarea').setValue('keep this prompt')
    await toggle.trigger('click')
    await toggle.trigger('click')
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('keep this prompt')
  })

  beforeEach(() => {
    getAvailableModels.mockReset()
    getAvailableModels.mockResolvedValue([
      { id: 'gemini-2.0-flash', display_name: 'Gemini 2.0 Flash' },
      { id: 'gemini-2.5-flash-image', display_name: 'Gemini 2.5 Flash Image' },
      { id: 'gemini-3.1-flash-image', display_name: 'Gemini 3.1 Flash Image' }
    ])
    copyToClipboard.mockReset()
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: vi.fn((key: string) => (key === 'auth_token' ? 'test-token' : null)),
        setItem: vi.fn(),
        removeItem: vi.fn(),
        clear: vi.fn()
      },
      configurable: true
    })
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"gemini-2.5-flash-image"}\n',
        'data: {"type":"image","image_url":"data:image/png;base64,QUJD","mime_type":"image/png"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('gemini 图片模型测试会携带提示词并渲染图片预览', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    const promptInput = wrapper.find('textarea.textarea-stub')
    expect(promptInput.exists()).toBe(true)
    await promptInput.setValue('draw a tiny orange cat astronaut')

    await wrapper.get('[data-test="row-test-gemini-3.1-flash-image"]').trigger('click')
    await vi.waitFor(() => {
      expect(global.fetch).toHaveBeenCalledTimes(1)
    })

    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'gemini-3.1-flash-image',
      prompt: 'draw a tiny orange cat astronaut'
    })

    const preview = wrapper.find('img[alt="test-image-1"]')
    expect(preview.exists()).toBe(true)
    expect(preview.attributes('src')).toBe('data:image/png;base64,QUJD')
  })

  it('grok 账号测试默认选择 Grok 模型', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'grok-4.3', display_name: 'Grok 4.3' },
      { id: 'grok-build-0.1', display_name: 'Grok Build 0.1' }
    ])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"grok-4.3"}\n',
        'data: {"type":"content","text":"ok"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 13,
      name: 'Grok Account',
      platform: 'grok',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    await wrapper.get('[data-test="row-test-grok-4.3"]').trigger('click')
    await vi.waitFor(() => {
      expect(global.fetch).toHaveBeenCalledTimes(1)
    })
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'grok-4.3',
      prompt: '',
      mode: 'text'
    })
  })

  it('OpenAI Compact 探测会携带 compact 测试模式', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' }
    ])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 42,
      name: 'OpenAI OAuth',
      platform: 'openai',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    ;(wrapper.vm as any).testMode = 'compact'
    await wrapper.get('[data-test="row-test-gpt-5.4"]').trigger('click')
    await vi.waitFor(() => {
      expect(global.fetch).toHaveBeenCalledTimes(1)
    })
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toMatchObject({
      model_id: 'gpt-5.4',
      prompt: '',
      mode: 'compact'
    })
  })

  it('测试全部模型会按目录顺序连打多次', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    const button = wrapper.find('[data-test="test-all-models"]')
    expect(button.exists()).toBe(true)
    await button.trigger('click')
    await vi.waitFor(() => {
      expect(global.fetch).toHaveBeenCalledTimes(3)
    })
    const bodies = (global.fetch as any).mock.calls.map(([, request]: [string, { body: string }]) => JSON.parse(request.body).model_id)
    expect([...bodies].sort()).toEqual([
      'gemini-3.1-flash-image',
      'gemini-2.5-flash-image',
      'gemini-2.0-flash'
    ].sort())
  })

  it.each([
    ['reopen', 'success'],
    ['reopen', 'error'],
    ['switch-account', 'success'],
    ['switch-account', 'error']
  ] as const)('独立语音 %s 后重跑，旧 %s 不污染新播放器或提前结束测试', async (action, outcome) => {
    const oldStream = createAbortIgnoringStream([
      { type: 'content', text: 'old partial' },
      { type: 'audio', audio_url: oldAudio, mime_type: 'audio/wav' }
    ])
    const newStream = createAbortIgnoringStream()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(oldStream.response).mockResolvedValueOnce(newStream.response))
    const wrapper = mountModal(grokAccount)
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('select.select-stub').setValue('tts')
    await wrapper.get('textarea').setValue('old prompt')
    await wrapper.get('[data-test="standalone-start"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('audio').attributes('src')).toBe(oldAudio)
    expect(wrapper.get('pre.account-test-standalone').text()).toBe('old partial')

    if (action === 'reopen') {
      const close = wrapper.findAll('button').find((button) => button.text() === 'common.close')
      await close!.trigger('click')
      expect(wrapper.emitted('close')).toHaveLength(1)
      expect(vi.mocked(fetch).mock.calls[0][1]?.signal?.aborted).toBe(true)
      expect(wrapper.find('audio').exists()).toBe(false)
      await wrapper.setProps({ show: false })
      await wrapper.setProps({ show: true })
    } else {
      await wrapper.setProps({ account: { ...grokAccount, id: 14, name: 'Grok B' } } as any)
    }
    await flushPromises()
    expect(vi.mocked(fetch).mock.calls[0][1]?.signal?.aborted).toBe(true)
    expect(oldStream.reader.cancel).toHaveBeenCalledOnce()
    expect(wrapper.find('audio').exists()).toBe(false)
    expect(wrapper.find('pre.account-test-standalone').exists()).toBe(false)
    expect(wrapper.find('[data-test="standalone-duration"]').exists()).toBe(false)
    await wrapper.get('select.select-stub').setValue('tts')
    await wrapper.get('textarea').setValue('fresh prompt')
    await wrapper.get('[data-test="standalone-start"]').trigger('click')
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(2)
    const [url, request] = vi.mocked(fetch).mock.calls[1]
    expect(url).toContain('/admin/accounts/' + (action === 'reopen' ? 13 : 14) + '/test')
    expect(JSON.parse(String(request?.body))).toEqual({ model_id: '', prompt: 'fresh prompt', mode: 'tts' })

    if (outcome === 'success') {
      oldStream.finish([
        { type: 'content', text: 'stale answer' },
        { type: 'audio', audio_url: oldAudio, mime_type: 'audio/wav' },
        { type: 'test_complete', success: true }
      ])
    } else {
      oldStream.fail(new Error('stale transport error'))
    }
    await flushPromises()
    expect(oldStream.reader.releaseLock).toHaveBeenCalledOnce()
    expect(request?.signal?.aborted).toBe(false)
    expect(wrapper.get<HTMLButtonElement>('[data-test="standalone-start"]').element.disabled).toBe(true)
    expect(wrapper.find('[data-test="standalone-duration"]').exists()).toBe(false)
    expect(wrapper.find('audio').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('stale')
    expect(wrapper.text()).not.toContain('old partial')

    newStream.finish([
      { type: 'content', text: 'fresh answer' },
      { type: 'audio', audio_url: newAudio, mime_type: 'audio/wav' },
      { type: 'test_complete', success: true }
    ])
    await flushPromises()
    expect(wrapper.get('pre.account-test-standalone').text()).toBe('fresh answer')
    expect(wrapper.findAll('audio')).toHaveLength(1)
    expect(wrapper.get('audio').attributes('src')).toBe(newAudio)
    expect(wrapper.get('[data-test="standalone-duration"]').text()).toMatch(/: \d+(?:\.\d+)?(?:ms|s)$/)
    expect(wrapper.get<HTMLButtonElement>('[data-test="standalone-start"]').element.disabled).toBe(false)
  })

  it.each(['success', 'error'] as const)('独立媒体新轮已完成，旧 %s 最后到达不覆盖正文、播放器和耗时', async (outcome) => {
    const oldStream = createAbortIgnoringStream()
    const newStream = createAbortIgnoringStream()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(oldStream.response).mockResolvedValueOnce(newStream.response))
    const wrapper = mountModal(grokAccount)
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('select.select-stub').setValue('tts')
    await wrapper.get('[data-test="standalone-start"]').trigger('click')
    await flushPromises()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('select.select-stub').setValue('tts')
    await wrapper.get('[data-test="standalone-start"]').trigger('click')
    await flushPromises()
    newStream.finish([
      { type: 'content', text: 'new completed' },
      { type: 'audio', audio_url: newAudio, mime_type: 'audio/wav' },
      { type: 'test_complete', success: true }
    ])
    await flushPromises()
    const duration = wrapper.get('[data-test="standalone-duration"]').text()
    if (outcome === 'success') {
      oldStream.finish([
        { type: 'content', text: 'stale completed' },
        { type: 'audio', audio_url: oldAudio, mime_type: 'audio/wav' },
        { type: 'test_complete', success: true }
      ])
    } else {
      oldStream.fail(new Error('stale error'))
    }
    await flushPromises()
    expect(wrapper.get('pre.account-test-standalone').text()).toBe('new completed')
    expect(wrapper.findAll('audio')).toHaveLength(1)
    expect(wrapper.get('audio').attributes('src')).toBe(newAudio)
    expect(wrapper.get('[data-test="standalone-duration"]').text()).toBe(duration)
    expect(wrapper.get<HTMLButtonElement>('[data-test="standalone-start"]').element.disabled).toBe(false)
    await wrapper.get('select.select-stub').setValue('search')
    expect(wrapper.find('audio').exists()).toBe(false)
    expect(wrapper.find('pre.account-test-standalone').exists()).toBe(false)
    expect(wrapper.find('[data-test="standalone-duration"]').exists()).toBe(false)
  })

  it('卸载单号弹窗会取消独立媒体读取并释放 reader', async () => {
    const stream = createAbortIgnoringStream()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(stream.response))
    const wrapper = mountModal(grokAccount)
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('select.select-stub').setValue('tts')
    await wrapper.get('[data-test="standalone-start"]').trigger('click')
    await flushPromises()
    const signal = vi.mocked(fetch).mock.calls[0][1]?.signal
    wrapper.unmount()
    expect(signal?.aborted).toBe(true)
    expect(stream.reader.cancel).toHaveBeenCalledOnce()
    stream.fail(new Error('aborted read settled after unmount'))
    await flushPromises()
    expect(stream.reader.releaseLock).toHaveBeenCalledOnce()
  })
})
