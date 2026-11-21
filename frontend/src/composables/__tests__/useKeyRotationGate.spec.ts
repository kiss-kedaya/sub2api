import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

const getStatus = vi.hoisted(() => vi.fn())

vi.mock('@/api/keyRotationGate', () => ({
  keyRotationGateAPI: { getStatus },
  KEY_ROTATION_REQUIRED_EVENT: 'key-rotation-required',
  KEY_ROTATION_REQUIRED_CODE: 'KEY_ROTATION_REQUIRED',
  KEY_ROTATION_REMEDIATION_PATH: '/keys',
  isKeyRotationRequiredError: () => false,
}))

const statusPayload = {
  enabled: true,
  required: true,
  cutoff: '2026-11-21T00:00:00+08:00',
  active_key_count: 2,
  compliant_key_count: 0,
  pending_key_count: 2,
  reason: 'rotation_required',
  error_code: 'KEY_ROTATION_REQUIRED',
  remediation_path: '/keys',
}

async function freshModule() {
  vi.resetModules()
  return import('../useKeyRotationGate')
}

describe('useKeyRotationGate', () => {
  beforeEach(() => {
    getStatus.mockReset()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('marks the gate as required from the status endpoint', async () => {
    getStatus.mockResolvedValue(statusPayload)
    const gate = await freshModule()

    await gate.ensureKeyRotationGate()

    expect(getStatus).toHaveBeenCalledTimes(1)
    expect(gate.keyRotationGateState().required).toBe(true)
    expect(gate.keyRotationGateState().status?.pending_key_count).toBe(2)
  })

  it('does not mark a compliant user as blocked', async () => {
    getStatus.mockResolvedValue({ ...statusPayload, required: false, pending_key_count: 0, compliant_key_count: 2 })
    const gate = await freshModule()

    await gate.ensureKeyRotationGate()

    expect(gate.keyRotationGateState().required).toBe(false)
  })

  it('caches the status until a forced refresh', async () => {
    getStatus.mockResolvedValue({ ...statusPayload, required: false })
    const gate = await freshModule()

    await gate.ensureKeyRotationGate()
    await gate.ensureKeyRotationGate()
    expect(getStatus).toHaveBeenCalledTimes(1)

    getStatus.mockResolvedValue(statusPayload)
    await gate.ensureKeyRotationGate(true)
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(gate.keyRotationGateState().required).toBe(true)
  })

  it('dedupes concurrent status reads into a single request', async () => {
    let resolveStatus!: (value: unknown) => void
    getStatus.mockImplementation(() => new Promise((resolve) => { resolveStatus = resolve }))
    const gate = await freshModule()

    const first = gate.ensureKeyRotationGate()
    const second = gate.ensureKeyRotationGate()
    resolveStatus({ ...statusPayload, required: false })
    await Promise.all([first, second])

    expect(getStatus).toHaveBeenCalledTimes(1)
  })

  it('keeps the previous decision when the status read fails', async () => {
    getStatus.mockRejectedValueOnce(new Error('network down'))
    const gate = await freshModule()

    await gate.ensureKeyRotationGate()
    expect(gate.keyRotationGateState().required).toBe(false)

    gate.markKeyRotationRequired()
    getStatus.mockRejectedValueOnce(new Error('network down'))
    await gate.ensureKeyRotationGate(true)
    expect(gate.keyRotationGateState().required).toBe(true)
  })

  it('marks the gate as required when the HTTP client broadcasts a blocked response', async () => {
    getStatus.mockResolvedValue({ ...statusPayload, required: false })
    const gate = await freshModule()

    mount(defineComponent({
      setup() {
        gate.useKeyRotationGate()
        return () => null
      },
    }))
    await flushPromises()

    window.dispatchEvent(new Event('key-rotation-required'))
    await flushPromises()

    expect(gate.keyRotationGateState().required).toBe(true)
  })

  it('resets the cached status on logout', async () => {
    getStatus.mockResolvedValue(statusPayload)
    const gate = await freshModule()

    await gate.ensureKeyRotationGate()
    expect(gate.keyRotationGateState().required).toBe(true)

    gate.resetKeyRotationGate()
    expect(gate.keyRotationGateState().required).toBe(false)
    expect(gate.keyRotationGateState().checked).toBe(false)
    expect(gate.keyRotationGateState().status).toBeNull()
  })
})
