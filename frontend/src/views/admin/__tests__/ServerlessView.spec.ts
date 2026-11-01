import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ServerlessView from '../ServerlessView.vue'
import * as api from '@/api/admin/serverless'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/api/admin/serverless', () => ({ getRegionalRouting: vi.fn(), saveRegionalRouting: vi.fn() }))
const status = (): api.RegionalStatus => ({ config: { revision: '', enabled: false, established: false, pods: [{ id: 'region-a', endpoint: 'https://gateway.example', enabled: false }], regions: [] }, pods: [], stats: {}, runtime: { node_id: '', gateway_node: false, secret_configured: false, redis_configured: true, private_network_configured: false } })
beforeEach(() => { vi.resetAllMocks(); vi.mocked(api.getRegionalRouting).mockResolvedValue(status()) })
describe('regional gateway configuration', () => {
  it('keeps routing disabled and does not accept a cluster secret', async () => {
    const view = mount(ServerlessView); await flushPromises()
    // The routing switch is a USwitch (<button role="switch">): unchecked and disabled without a cluster secret.
    const enabled = view.get('[data-testid="serverless-enabled"]'); expect(enabled.attributes('role')).toBe('switch'); expect(enabled.attributes('aria-checked')).toBe('false'); expect(enabled.attributes('disabled')).toBeDefined()
    expect(view.find('input[type="password"]').exists()).toBe(false); expect(api.saveRegionalRouting).not.toHaveBeenCalled(); view.unmount()
  })
  it('retains configured origins after a failed refresh', async () => {
    const view = mount(ServerlessView); await flushPromises()
    vi.mocked(api.getRegionalRouting).mockRejectedValueOnce(new Error('temporary'))
    await view.findAll('button').find(button => button.text() === 'serverless.refresh')!.trigger('click'); await flushPromises()
    expect((view.find('input[type="url"]').element as HTMLInputElement).value).toBe('https://gateway.example'); expect(view.find('[role="alert"]').exists()).toBe(true); view.unmount()
  })
})
