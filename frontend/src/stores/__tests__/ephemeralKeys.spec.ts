import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { useEphemeralKeyStore } from '../ephemeralKeys'

describe('ephemeral API key store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('remembers a created key for this tab and recalls the exact plaintext', () => {
    const store = useEphemeralKeyStore()
    store.remember(7, 'sk-new-key')

    expect(store.has(7)).toBe(true)
    expect(store.recall(7)).toBe('sk-new-key')
    expect(store.recall(8)).toBeUndefined()
    expect(store.has(8)).toBe(false)
  })

  it('overwrites the same id on rotation so the stale plaintext disappears', () => {
    const store = useEphemeralKeyStore()
    store.remember(7, 'sk-old-key')
    store.remember(7, 'sk-rotated-key')

    expect(store.recall(7)).toBe('sk-rotated-key')
  })

  it('forgets one key without touching the others', () => {
    const store = useEphemeralKeyStore()
    store.remember(7, 'sk-seven')
    store.remember(9, 'sk-nine')

    store.forget(7)

    expect(store.has(7)).toBe(false)
    expect(store.recall(9)).toBe('sk-nine')
  })

  it('clears everything on logout / refresh boundary', () => {
    const store = useEphemeralKeyStore()
    store.remember(7, 'sk-seven')
    store.remember(9, 'sk-nine')

    store.clear()

    expect(store.has(7)).toBe(false)
    expect(store.has(9)).toBe(false)
  })

  it('ignores invalid ids and empty plaintexts', () => {
    const store = useEphemeralKeyStore()
    store.remember(Number.NaN, 'sk-nan')
    store.remember(7, '')

    expect(store.has(Number.NaN)).toBe(false)
    expect(store.has(7)).toBe(false)
  })

  it('is reactive: a component reading recall(id) sees a later remember', async () => {
    const store = useEphemeralKeyStore()
    const { effect } = await import('vue')
    let seen: string | undefined
    effect(() => {
      seen = store.recall(7)
    })
    expect(seen).toBeUndefined()

    store.remember(7, 'sk-reactive')
    expect(seen).toBe('sk-reactive')

    store.clear()
    expect(seen).toBeUndefined()
  })
})