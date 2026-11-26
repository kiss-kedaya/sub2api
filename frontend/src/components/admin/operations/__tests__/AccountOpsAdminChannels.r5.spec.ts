import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import AccountOpsAdminChannels from '../AccountOpsAdminChannels.vue'
import type { AccountOpsConfig } from '@/api/admin/accountOps'

vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))

const config: AccountOpsConfig = {
  enabled: true, recipient: '', use_admin_email: true, use_admin_bark: false,
  balance_low: true, weekly_quota: true, cooldown_minutes: 60,
  webhooks: [], balance_thresholds: [], quota_thresholds: []
}

const create = (selected = false, available = false) => mount(AccountOpsAdminChannels, {
  props: {
    config: { ...config, use_admin_bark: selected },
    status: {
      email_available: true, email_masked: 'o***r@example.invalid', email_unavailable_reason: '',
      bark_configured: available, bark_enabled: available, bark_available: available,
      bark_unavailable_reason: available ? '' : 'bark_unconfigured'
    },
    smtpConfigured: true, busy: false, testingId: null
  },
  global: {
    plugins: [createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }] })]
  }
})

describe('R5 effective administrator channels', () => {
  it('shows an unavailable implicit Bark as unused and explains why', () => {
    const wrapper = create()
    expect(wrapper.get('[data-testid="use-admin-bark"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-testid="admin-bark-row"]').text()).toContain('accountOps.channelUnused')
    expect(wrapper.get('[data-testid="admin-bark-row"]').text()).toContain('accountOps.adminReasons.bark_unconfigured')
    expect(wrapper.get('[data-testid="test-admin-bark"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="configure-admin-bark"]').attributes('href')).toBe('/profile?tab=notifications')
    wrapper.unmount()
  })

  it('only displays backend selections, even when profile availability differs', async () => {
    const wrapper = create(false, true)
    expect(wrapper.get('[data-testid="use-admin-bark"]').attributes('aria-checked')).toBe('false')
    await wrapper.setProps({ config: { ...config, use_admin_bark: true } })
    expect(wrapper.get('[data-testid="use-admin-bark"]').attributes('aria-checked')).toBe('true')
    await wrapper.setProps({ config: { ...config, use_admin_bark: true, webhooks: [{ id: 'off', provider: 'bark', enabled: false, url_configured: true }] } })
    expect(wrapper.get('[data-testid="use-admin-email"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-testid="use-admin-bark"]').attributes('aria-checked')).toBe('true')
    wrapper.unmount()
  })
})
