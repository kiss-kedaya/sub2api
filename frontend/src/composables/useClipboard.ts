import { ref } from 'vue'
import { useAppStore } from '@/stores/app'
import { i18n } from '@/i18n'

/**
 * 检测是否支持 Clipboard API（需要安全上下文：HTTPS/localhost）
 */
function isClipboardSupported(): boolean {
  return !!(navigator.clipboard && window.isSecureContext)
}

/**
 * 降级方案：使用 textarea + execCommand
 * 使用 textarea 而非 input，以正确处理多行文本；在弹窗打开时挂载到弹窗容器内，避免 focus-trap / inert 拦截。
 */
function fallbackCopy(text: string): boolean {
  if (typeof document === 'undefined') return false
  const activeEl = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const container =
    (activeEl?.closest?.('[role="dialog"], .modal-overlay') as HTMLElement | null) ?? document.body
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.readOnly = false
  textarea.inputMode = 'none'
  textarea.setAttribute('aria-hidden', 'true')
  textarea.style.cssText =
    'position:fixed;left:-9999px;top:0;width:1px;height:1px;font-size:12pt;opacity:0;pointer-events:none'
  container.appendChild(textarea)
  textarea.focus({ preventScroll: true })
  textarea.select()
  textarea.setSelectionRange(0, textarea.value.length)
  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    container.removeChild(textarea)
    activeEl?.focus?.({ preventScroll: true })
  }
}

export function useClipboard() {
  const { t } = i18n.global
  const appStore = useAppStore()
  const copied = ref(false)

  const copyToClipboard = async (
    text: string,
    successMessage?: string
  ): Promise<boolean> => {
    if (!text) return false

    let success = false

    if (isClipboardSupported()) {
      try {
        await navigator.clipboard.writeText(text)
        success = true
      } catch {
        success = fallbackCopy(text)
      }
    } else {
      success = fallbackCopy(text)
    }

    if (success) {
      copied.value = true
      appStore.showSuccess(successMessage || t('common.copiedToClipboard'))
      setTimeout(() => {
        copied.value = false
      }, 2000)
    } else {
      appStore.showError(t('common.copyFailed'))
    }

    return success
  }

  return { copied, copyToClipboard }
}
