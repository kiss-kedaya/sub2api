import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ProfilePersonalApiKeyCard from '@/components/user/profile/ProfilePersonalApiKeyCard.vue'

const {
  getPersonalApiKeyMock,
  createOrRegeneratePersonalApiKeyMock,
  deletePersonalApiKeyMock,
  getVerificationFactorsMock,
  showSuccessMock,
  showErrorMock,
  factorResetMock,
} = vi.hoisted(() => ({
  getPersonalApiKeyMock: vi.fn(),
  createOrRegeneratePersonalApiKeyMock: vi.fn(),
  deletePersonalApiKeyMock: vi.fn(),
  getVerificationFactorsMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showErrorMock: vi.fn(),
  factorResetMock: vi.fn(),
}))

vi.mock('@/api', () => ({
  userAPI: {
    getPersonalApiKey: getPersonalApiKeyMock,
    createOrRegeneratePersonalApiKey: createOrRegeneratePersonalApiKeyMock,
    deletePersonalApiKey: deletePersonalApiKeyMock,
  },
  keysAPI: {
    getVerificationFactors: getVerificationFactorsMock,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: showSuccessMock,
    showError: showErrorMock,
  }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
    }),
  }
})

const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: '<div v-if="show" class="dialog-stub"><slot name="description" /><slot /><slot name="footer" /></div>',
}

const KeyFactorVerificationStub = {
  props: ['factors'],
  methods: {
    buildVerification: () => ({ method: 'password', password: 'test-password' }),
    reset: () => factorResetMock(),
  },
  template: '<div data-test="factor-stub" />',
}

const ButtonStub = {
  props: ['disabled', 'loading', 'label'],
  emits: ['click'],
  template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot>{{ label }}</slot></button>',
}

function mountCard() {
  return mount(ProfilePersonalApiKeyCard, {
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        KeyFactorVerification: KeyFactorVerificationStub,
        UButton: ButtonStub,
        UBadge: { template: '<span class="badge"><slot /></span>' },
        UIcon: true,
      },
    },
  })
}

describe('ProfilePersonalApiKeyCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getVerificationFactorsMock.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
    })
  })

  it('renders empty state, verifies identity on create, reveals secret once, and clears secret on close', async () => {
    vi.useFakeTimers()
    try {
      getPersonalApiKeyMock.mockResolvedValue({
        exists: false,
        rate_limit_rpm: 240,
        write_rate_limit_rpm: 60,
      })
      const plaintext = `uak_${'ab'.repeat(32)}`
      createOrRegeneratePersonalApiKeyMock.mockResolvedValue({
        key: plaintext,
        regenerated: false,
        status: {
          exists: true,
          key_prefix: 'uak_abababab...abab',
          created_at: '2026-11-27T08:00:00Z',
          updated_at: '2026-11-27T08:00:00Z',
          last_used_at: null,
          last_used_ip: '',
          rate_limit_rpm: 240,
          write_rate_limit_rpm: 60,
        },
      })

      const wrapper = mountCard()
      await flushPromises()

      expect(wrapper.find('[data-test="personal-api-key-empty"]').exists()).toBe(true)
      await wrapper.get('[data-test="personal-api-key-create"]').trigger('click')
      await flushPromises()

      expect(wrapper.find('[data-test="personal-api-key-verify-dialog"]').exists()).toBe(true)
      await wrapper.get('[data-test="personal-api-key-verify-confirm"]').trigger('click')
      await flushPromises()

      expect(createOrRegeneratePersonalApiKeyMock).toHaveBeenCalledWith({
        method: 'password',
        password: 'test-password',
      })
      expect(wrapper.get('[data-test="personal-api-key-reveal-value"]').text()).toBe(plaintext)
      expect(wrapper.get('[data-test="personal-api-key-prefix"]').text()).toBe('uak_abababab...abab')

      // Unlock close button after brief guard and close: plaintext must vanish from DOM.
      vi.advanceTimersByTime(1000)
      await flushPromises()
      await wrapper.get('[data-test="personal-api-key-reveal-done"]').trigger('click')
      await flushPromises()

      expect(wrapper.find('[data-test="personal-api-key-reveal-dialog"]').exists()).toBe(false)
      expect(wrapper.html()).not.toContain(plaintext)
    } finally {
      vi.useRealTimers()
    }
  })

  it('toggles integration examples and revokes an existing key on delete confirmation', async () => {
    getPersonalApiKeyMock.mockResolvedValue({
      exists: true,
      key_prefix: 'uak_12345678...90ab',
      created_at: '2026-11-27T08:00:00Z',
      updated_at: '2026-11-27T08:00:00Z',
      last_used_at: '2026-11-27T08:15:00Z',
      last_used_ip: '203.0.113.5',
      rate_limit_rpm: 240,
      write_rate_limit_rpm: 60,
    })
    deletePersonalApiKeyMock.mockResolvedValue({
      deleted: true,
      message: 'Personal API key deleted successfully',
    })

    const wrapper = mountCard()
    await flushPromises()

    expect(wrapper.find('[data-test="personal-api-key-active"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="personal-api-key-prefix"]').text()).toBe('uak_12345678...90ab')

    await wrapper.get('[data-test="personal-api-key-toggle-examples"]').trigger('click')
    expect(wrapper.find('[data-test="personal-api-key-examples"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="personal-api-key-examples"]').text()).toContain('Authorization: Bearer uak_YOUR_PERSONAL_KEY')
    expect(wrapper.get('[data-test="personal-api-key-examples"]').text()).toContain('"group_id":1')

    await wrapper.get('[data-test="personal-api-key-mode-apikey"]').trigger('click')
    expect(wrapper.get('[data-test="personal-api-key-examples"]').text()).toContain('X-API-Key: uak_YOUR_PERSONAL_KEY')

    await wrapper.get('[data-test="personal-api-key-delete"]').trigger('click')
    await wrapper.get('[data-test="personal-api-key-delete-confirm"]').trigger('click')
    await flushPromises()

    expect(deletePersonalApiKeyMock).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="personal-api-key-empty"]').exists()).toBe(true)
    expect(showSuccessMock).toHaveBeenCalled()
  })

  it('resets factor verification inputs when verification fails on create', async () => {
    getPersonalApiKeyMock.mockResolvedValue({
      exists: false,
      rate_limit_rpm: 240,
      write_rate_limit_rpm: 60,
    })
    createOrRegeneratePersonalApiKeyMock.mockRejectedValue({
      reason: 'VERIFY_PASSWORD_INVALID',
      message: 'password is incorrect',
    })

    const wrapper = mountCard()
    await flushPromises()

    await wrapper.get('[data-test="personal-api-key-create"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="personal-api-key-verify-confirm"]').trigger('click')
    await flushPromises()

    expect(factorResetMock).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="personal-api-key-verify-error"]').text()).toBe('keys.factor.passwordInvalid')
  })
})
