<template>
  <!-- 创建/轮换成功后的「再复制一次」浮条：固定右下角，不是 modal。
       明文只从内存里的 ephemeral store 取，绝不读写 localStorage/sessionStorage。 -->
  <div
    v-if="visible && secret"
    class="ephemeral-key-bar"
    role="status"
    aria-live="polite"
    data-test="ephemeral-key-bar"
  >
    <div class="ephemeral-key-bar__icon">
      <UIcon name="i-lucide-key-round" class="size-4" aria-hidden="true" />
    </div>
    <div class="min-w-0 flex-1">
      <p class="ephemeral-key-bar__title">{{ t('keys.ephemeralBar.title') }}</p>
      <p class="ephemeral-key-bar__hint">{{ t('keys.ephemeralBar.hint', { minutes: expiresInMinutes }) }}</p>
    </div>
    <UButton
      color="primary"
      size="xs"
      :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'"
      :label="copied ? t('common.copied') : t('keys.ephemeralBar.copy')"
      data-test="ephemeral-key-copy"
      @click="copyKey"
    />
    <button
      type="button"
      class="ephemeral-key-bar__close"
      :aria-label="t('common.close')"
      data-test="ephemeral-key-close"
      @click="dismiss"
    >
      <UIcon name="i-lucide-x" class="size-4" aria-hidden="true" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useClipboard } from '@/composables/useClipboard'
import { useEphemeralKeyStore } from '@/stores/ephemeralKeys'

const DEFAULT_TTL_MS = 5 * 60 * 1000
const COPIED_RESET_MS = 2000

const props = withDefaults(
  defineProps<{
    /** API key id whose plaintext lives in the ephemeral store. */
    keyId: number
    /** How long the bar stays (default 5 minutes). */
    ttlMs?: number
  }>(),
  { ttlMs: DEFAULT_TTL_MS }
)

const emit = defineEmits<{ (event: 'expired'): void }>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const ephemeralKeys = useEphemeralKeyStore()

const visible = ref(true)
const copied = ref(false)
let expiryTimer: ReturnType<typeof setTimeout> | null = null
let copiedTimer: ReturnType<typeof setTimeout> | null = null

// The store is the only source of the plaintext: the parent never passes it in.
const secret = computed(() => ephemeralKeys.recall(props.keyId) ?? '')
const expiresInMinutes = Math.max(1, Math.round(props.ttlMs / 60000))

const clearTimers = () => {
  if (expiryTimer) {
    clearTimeout(expiryTimer)
    expiryTimer = null
  }
  if (copiedTimer) {
    clearTimeout(copiedTimer)
    copiedTimer = null
  }
}

const startExpiry = () => {
  if (expiryTimer) clearTimeout(expiryTimer)
  expiryTimer = setTimeout(() => {
    expiryTimer = null
    visible.value = false
    emit('expired')
  }, props.ttlMs)
}

const copyKey = async () => {
  if (!secret.value) return
  const ok = await copyToClipboard(secret.value)
  if (!ok) return
  copied.value = true
  if (copiedTimer) clearTimeout(copiedTimer)
  copiedTimer = setTimeout(() => {
    copied.value = false
    copiedTimer = null
  }, COPIED_RESET_MS)
}

const dismiss = () => {
  visible.value = false
  clearTimers()
  emit('expired')
}

onMounted(startExpiry)
onUnmounted(clearTimers)

// A rotation can reuse the same mount point (same id, new plaintext): restart the
// five-minute window so the refreshed receipt is not cut short by the old timer.
watch(
  () => props.keyId,
  () => {
    visible.value = true
    copied.value = false
    startExpiry()
  }
)
</script>

<style scoped>
.ephemeral-key-bar {
  position: fixed;
  right: 20px;
  bottom: 20px;
  z-index: 60;
  display: flex;
  align-items: center;
  gap: 12px;
  max-width: min(460px, calc(100vw - 32px));
  padding: 12px 14px;
  border-radius: 12px;
  background: var(--ui-bg, #fff);
  box-shadow: 0 10px 30px rgb(0 0 0 / 18%), 0 2px 8px rgb(0 0 0 / 10%);
  border: 1px solid var(--ui-border, rgb(0 0 0 / 10%));
}

.ephemeral-key-bar__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  flex: none;
  border-radius: 999px;
  color: var(--ui-primary, #2563eb);
  background: color-mix(in srgb, var(--ui-primary, #2563eb) 12%, transparent);
}

.ephemeral-key-bar__title {
  font-size: 13px;
  font-weight: 600;
  line-height: 18px;
  color: var(--ui-text-highlighted, #111827);
}

.ephemeral-key-bar__hint {
  margin-top: 2px;
  font-size: 11px;
  line-height: 15px;
  color: var(--ui-text-muted, #6b7280);
}

.ephemeral-key-bar__close {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  flex: none;
  border-radius: 6px;
  color: var(--ui-text-muted, #6b7280);
}

.ephemeral-key-bar__close:hover {
  background: var(--ui-bg-elevated, rgb(0 0 0 / 6%));
  color: var(--ui-text, #111827);
}

@media (prefers-reduced-motion: no-preference) {
  .ephemeral-key-bar {
    animation: ephemeral-key-bar-in 180ms ease-out;
  }
}

@keyframes ephemeral-key-bar-in {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
</style>
