import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick, reactive, ref, type Ref } from 'vue'

import {
  clearUserDialogDrafts,
  currentDialogDraftUserId,
  useDialogDraft
} from '@/composables/useDialogDraft'

/**
 * Minimal in-memory storage so the tests never depend on a real browser. It deliberately
 * does not `implements Storage`: lib.dom gives that interface an index signature, which a
 * class has to declare as well. Assigning it where a Storage is expected is enough.
 */
class MemoryStorage {
  private map = new Map<string, string>()
  get length(): number {
    return this.map.size
  }
  clear(): void {
    this.map.clear()
  }
  getItem(key: string): string | null {
    return this.map.has(key) ? this.map.get(key)! : null
  }
  key(index: number): string | null {
    return Array.from(this.map.keys())[index] ?? null
  }
  removeItem(key: string): void {
    this.map.delete(key)
  }
  setItem(key: string, value: string): void {
    this.map.set(key, String(value))
  }
  /** Everything currently persisted, for the leak assertions. */
  dump(): string {
    return JSON.stringify(Object.fromEntries(this.map.entries()))
  }
}

/** The composable only calls the six Storage members, so this cast is honest. */
const asStorage = (store: MemoryStorage | null): Storage | null => store as unknown as Storage | null

interface Form extends Record<string, unknown> {
  amount: number | null
  note: string
  password: string
  apiKey: string
  totp: string
}

const makeForm = (overrides: Partial<Form> = {}): Form =>
  reactive<Form>({
    amount: null,
    note: '',
    password: '',
    apiKey: '',
    totp: '',
    ...overrides
  })

const DRAFT_KEYS = ['amount', 'note'] as const

function mount(
  form: Form,
  options: {
    key?: string
    open?: Ref<boolean>
    userId?: string | number | null
    storage?: MemoryStorage | null
    signature?: () => string | null
    enabled?: () => boolean
    fields?: readonly (keyof Form & string)[]
  } = {}
) {
  const open = options.open ?? ref(false)
  // One storage instance for the whole mount: handing back a fresh one per read would make
  // every save land somewhere the restore never looks.
  const storage = options.storage === undefined ? new MemoryStorage() : options.storage
  const draft = useDialogDraft<Form>({
    key: () => options.key ?? 'test-dialog',
    fields: options.fields ?? DRAFT_KEYS,
    state: form,
    open: () => open.value,
    signature: options.signature,
    enabled: options.enabled,
    userId: () => (options.userId === undefined ? 42 : options.userId),
    storage: () => asStorage(storage)
  })
  return { draft, open, storage }
}

describe('useDialogDraft', () => {
  beforeEach(() => {
    vi.useRealTimers()
  })

  it('neither writes nor restores while the form is in a shape the draft cannot represent', async () => {
    const storage = new MemoryStorage()
    const form = makeForm()
    const open = ref(false)
    const enabled = ref(true)
    mount(form, { storage, open, enabled: () => enabled.value })

    // A draft exists from an earlier, representable edit.
    open.value = true
    await nextTick()
    form.amount = 25
    open.value = false
    await nextTick()
    expect(storage.length).toBe(1)

    // The dialog is now in the other mode: closing it must remove the draft, not refresh it.
    enabled.value = false
    form.amount = null
    open.value = true
    await nextTick()
    expect(form.amount).toBeNull()
    form.note = 'typed in the other mode'
    open.value = false
    await nextTick()
    expect(storage.length).toBe(0)
  })

  it('restores a draft after the dialog is closed without submitting', async () => {
    const storage = new MemoryStorage()
    const form = makeForm()
    const open = ref(false)
    mount(form, { storage, open })

    open.value = true
    await nextTick()
    form.amount = 25
    form.note = 'half-finished batch'

    // Clicking the veil closes the dialog without submitting.
    open.value = false
    await nextTick()

    form.amount = null
    form.note = ''
    open.value = true
    await nextTick()

    expect(form.amount).toBe(25)
    expect(form.note).toBe('half-finished batch')
  })

  it('flags the restored notice only when a draft was actually applied', async () => {
    const storage = new MemoryStorage()
    const form = makeForm()
    const open = ref(false)
    const { draft } = mount(form, { storage, open })

    open.value = true
    await nextTick()
    expect(draft.restored.value).toBe(false)

    form.note = 'kept'
    open.value = false
    await nextTick()

    form.note = ''
    open.value = true
    await nextTick()
    expect(draft.restored.value).toBe(true)
  })

  it('clears the draft after a successful submit so the next open starts empty', async () => {
    const storage = new MemoryStorage()
    const form = makeForm()
    const open = ref(false)
    const { draft } = mount(form, { storage, open })

    open.value = true
    await nextTick()
    form.amount = 99
    draft.markSubmitted()

    open.value = false
    await nextTick()
    expect(storage.dump()).not.toContain('99')

    open.value = true
    await nextTick()
    expect(form.amount).toBe(99) // the component keeps its own value
    expect(draft.restored.value).toBe(false) // but nothing was restored
  })

  it('shows the notice even when the form still holds the saved values', async () => {
    // Redeeming a code keeps `generateForm` populated after the veil closes it, so the
    // restored values equal the ones already in state. The user still has a draft to
    // discard, so the notice must appear.
    const storage = new MemoryStorage()
    const form = makeForm()
    const open = ref(false)
    const { draft } = mount(form, { storage, open })

    open.value = true
    await nextTick()
    form.amount = 12
    form.note = 'kept as-is'
    open.value = false
    await nextTick()

    // Deliberately do NOT clear the form first.
    open.value = true
    await nextTick()

    expect(form.amount).toBe(12)
    expect(form.note).toBe('kept as-is')
    expect(draft.restored.value).toBe(true)
  })

  it('does not recreate the draft when the dialog is closed right after a discard', async () => {
    // The real flow: click discard, then click the veil. The close must not write the
    // values the discard just dropped back into storage.
    const storage = new MemoryStorage()
    const form = makeForm()
    const open = ref(false)
    const { draft } = mount(form, { storage, open })

    open.value = true
    await nextTick()
    form.amount = 44
    form.note = 'discard me'
    open.value = false
    await nextTick()

    open.value = true
    await nextTick()
    expect(draft.restored.value).toBe(true)

    draft.discard()
    // The component resets its own form right after discarding.
    form.amount = null
    form.note = ''
    await nextTick()

    open.value = false
    await nextTick()
    expect(storage.dump()).not.toContain('discard me')

    open.value = true
    await nextTick()
    expect(draft.restored.value).toBe(false)
    expect(storage.dump()).not.toContain('discard me')
  })

  it('autosaves again after the user types following a discard', async () => {
    const storage = new MemoryStorage()
    const form = makeForm()
    const open = ref(false)
    const { draft } = mount(form, { storage, open })

    open.value = true
    await nextTick()
    form.note = 'first attempt'
    open.value = false
    await nextTick()

    open.value = true
    await nextTick()
    draft.discard()
    form.note = ''
    await nextTick()

    // Second attempt in the same open dialog.
    form.note = 'second attempt'
    open.value = false
    await nextTick()

    expect(storage.dump()).toContain('second attempt')
  })

  it('drops the stored draft when the user discards it', async () => {
    const storage = new MemoryStorage()
    const form = makeForm()
    const open = ref(false)
    const { draft } = mount(form, { storage, open })

    open.value = true
    await nextTick()
    form.note = 'throw me away'
    open.value = false
    await nextTick()

    draft.discard()
    expect(draft.restored.value).toBe(false)

    form.note = ''
    open.value = true
    await nextTick()
    expect(form.note).toBe('')
    expect(storage.dump()).not.toContain('throw me away')
  })

  it('keeps drafts invisible to another user on the same browser', async () => {
    const storage = new MemoryStorage()
    const alice = makeForm()
    const aliceOpen = ref(false)
    mount(alice, { storage, open: aliceOpen, userId: 1 })

    aliceOpen.value = true
    await nextTick()
    alice.note = 'alice private draft'
    aliceOpen.value = false
    await nextTick()

    const bob = makeForm()
    const bobOpen = ref(false)
    mount(bob, { storage, open: bobOpen, userId: 2 })

    bobOpen.value = true
    await nextTick()
    expect(bob.note).toBe('')
  })

  it('does not write anything when the user id is unknown', async () => {
    const storage = new MemoryStorage()
    const form = makeForm()
    const open = ref(false)
    mount(form, { storage, open, userId: null })

    open.value = true
    await nextTick()
    form.note = 'no identity'
    open.value = false
    await nextTick()

    expect(storage.length).toBe(0)
  })

  it('discards a stale draft when the edited object moved on', async () => {
    const storage = new MemoryStorage()
    let updatedAt = '2026-11-11T00:00:00Z'
    const form = makeForm()
    const open = ref(false)
    mount(form, { storage, open, signature: () => updatedAt })

    open.value = true
    await nextTick()
    form.note = 'stale value'
    open.value = false
    await nextTick()

    // Someone else saved the object while this dialog was closed.
    updatedAt = '2026-11-11T05:00:00Z'
    form.note = ''
    open.value = true
    await nextTick()

    expect(form.note).toBe('')
    expect(storage.dump()).not.toContain('stale value')
  })

  it('restores when the signature still matches', async () => {
    const storage = new MemoryStorage()
    const form = makeForm()
    const open = ref(false)
    const { draft } = mount(form, { storage, open, signature: () => 'v1' })

    open.value = true
    await nextTick()
    form.note = 'still valid'
    open.value = false
    await nextTick()

    form.note = ''
    open.value = true
    await nextTick()
    expect(form.note).toBe('still valid')
    expect(draft.restored.value).toBe(true)
  })

  it('never persists fields outside the whitelist', async () => {
    const storage = new MemoryStorage()
    const form = makeForm()
    const open = ref(false)
    mount(form, { storage, open })

    open.value = true
    await nextTick()
    form.amount = 5
    form.note = 'visible'
    form.password = 'hunter2-secret'
    form.apiKey = 'sk-live-do-not-store'
    form.totp = '123456'
    open.value = false
    await nextTick()

    const persisted = storage.dump()
    expect(persisted).toContain('visible')
    expect(persisted).not.toContain('hunter2-secret')
    expect(persisted).not.toContain('sk-live-do-not-store')
    expect(persisted).not.toContain('123456')
  })

  it('ignores injected sensitive keys smuggled into storage', async () => {
    const storage = new MemoryStorage()
    // A hostile value written under the same user key must not be read back into state.
    storage.setItem(
      'sub2api:dialog-draft:v1:42:test-dialog',
      JSON.stringify({ signature: null, fields: { note: 'ok', password: 'leaked' } })
    )
    const form = makeForm()
    const open = ref(false)
    mount(form, { storage, open })

    open.value = true
    await nextTick()

    expect(form.note).toBe('ok')
    expect(form.password).toBe('')
  })

  it('stays quiet when sessionStorage is unavailable', async () => {
    const form = makeForm()
    const open = ref(false)
    const { draft } = mount(form, { storage: null, open })

    expect(() => {
      open.value = true
      form.note = 'anything'
      draft.save()
      draft.restore()
      draft.discard()
    }).not.toThrow()
  })

  it('stays quiet when the storage implementation throws', async () => {
    const hostile: Storage = {
      length: 0,
      clear: () => {
        throw new Error('denied')
      },
      getItem: () => {
        throw new Error('denied')
      },
      key: () => {
        throw new Error('denied')
      },
      removeItem: () => {
        throw new Error('denied')
      },
      setItem: () => {
        throw new Error('denied')
      }
    }
    const form = makeForm()
    const open = ref(false)
    const { draft } = mount(form, { storage: hostile, open })

    expect(() => {
      open.value = true
      form.note = 'still fine'
      open.value = false
      draft.restore()
      draft.discard()
    }).not.toThrow()
    expect(() => clearUserDialogDrafts(42, hostile)).not.toThrow()
  })

  it('survives a corrupted stored value', async () => {
    const storage = new MemoryStorage()
    storage.setItem('sub2api:dialog-draft:v1:42:test-dialog', 'not json {{{')
    const form = makeForm()
    const open = ref(false)
    const { draft } = mount(form, { storage, open })

    open.value = true
    await nextTick()
    expect(form.note).toBe('')
    expect(draft.restored.value).toBe(false)
  })
})

describe('clearUserDialogDrafts', () => {
  it('removes only the drafts of the signing-out user', async () => {
    const storage = new MemoryStorage()
    storage.setItem('sub2api:dialog-draft:v1:1:renew', '{}')
    storage.setItem('sub2api:dialog-draft:v1:2:renew', '{}')

    clearUserDialogDrafts(1, asStorage(storage))

    expect(storage.getItem('sub2api:dialog-draft:v1:1:renew')).toBeNull()
    expect(storage.getItem('sub2api:dialog-draft:v1:2:renew')).not.toBeNull()
  })

  it('is a no-op without an identity or storage', () => {
    const storage = new MemoryStorage()
    storage.setItem('sub2api:dialog-draft:v1:1:renew', '{}')
    expect(() => clearUserDialogDrafts(null, asStorage(storage))).not.toThrow()
    expect(() => clearUserDialogDrafts(1, null)).not.toThrow()
    expect(storage.length).toBe(1)
  })
})

describe('currentDialogDraftUserId', () => {
  it('returns null when no auth store is active', () => {
    expect(currentDialogDraftUserId()).toBeNull()
  })
})
