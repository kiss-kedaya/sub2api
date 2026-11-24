<script setup lang="ts">
// 页面版本过期提示：常驻、不能关掉（强制刷新的意思），但不挡内容，放在底部居中。
// 下一次页面跳转时会直接整页加载，所以用户最多看到它一小会儿（见 useAppUpdate）。
import { useI18n } from 'vue-i18n'
import { useAppUpdate } from '@/composables/useAppUpdate'

const { t } = useI18n()
const { updateAvailable, reloadNow } = useAppUpdate()
</script>

<template>
  <Transition name="app-update">
    <div
      v-if="updateAvailable"
      class="app-update-bar"
      role="status"
      aria-live="polite"
      data-testid="app-update-banner"
    >
      <span class="app-update-text">{{ t('appUpdate.available') }}</span>
      <button
        type="button"
        class="app-update-action"
        data-testid="app-update-refresh"
        @click="reloadNow"
      >
        {{ t('appUpdate.refresh') }}
      </button>
    </div>
  </Transition>
</template>

<style scoped>
/* 居中用 margin:auto 而不是 translateX，留出 transform 给入场动画。
   层级 60：在对话框（50）之上，在 Nuxt UI 的 toaster 之下。 */
.app-update-bar {
  position: fixed;
  inset-inline: 0;
  bottom: 1rem;
  z-index: 60;
  display: flex;
  align-items: center;
  gap: 0.75rem;
  width: max-content;
  max-width: calc(100vw - 2rem);
  margin-inline: auto;
  padding: 0.375rem 0.375rem 0.375rem 1rem;
  border-radius: 9999px;
  background: var(--ui-bg-elevated);
  color: var(--ui-text-highlighted);
  font-size: 14px;
  line-height: 1.3;
  box-shadow:
    0 4px 6px -1px rgb(0 0 0 / 0.1),
    0 2px 4px -2px rgb(0 0 0 / 0.1),
    inset 0 0 0 1px var(--ui-border);
}

.app-update-text {
  min-width: 0;
  color: var(--ui-text-highlighted);
}

.app-update-action {
  flex-shrink: 0;
  height: 2rem;
  padding: 0 1rem;
  border-radius: 9999px;
  background: var(--ui-primary);
  color: var(--ui-bg);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: transform 150ms cubic-bezier(0.23, 1, 0.32, 1);
}

.app-update-action:active {
  transform: scale(0.97);
}

.app-update-action:focus-visible {
  outline: 2px solid var(--ui-primary);
  outline-offset: 2px;
}

/* 入场只淡入并上移 8px；它不会自己消失，所以没有离场动画。 */
.app-update-enter-active {
  transition:
    opacity 200ms cubic-bezier(0.23, 1, 0.32, 1),
    transform 200ms cubic-bezier(0.23, 1, 0.32, 1);
}

.app-update-enter-from {
  opacity: 0;
  transform: translateY(8px);
}

@media (prefers-reduced-motion: reduce) {
  .app-update-enter-active {
    transition: opacity 120ms linear;
  }

  .app-update-enter-from {
    transform: none;
  }

  .app-update-action {
    transition: none;
  }

  .app-update-action:active {
    transform: none;
  }
}
</style>
