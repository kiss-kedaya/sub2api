<script setup lang="ts">
// Quiet 步骤头：左边「第 N 步 / 共 M 步」，右边点状进度。和注册页用同一套样式（auth-quiet.css），
// 注册页自己内联了一份同样的标记；这里给对话框等其它流程复用，不重复写样式。
import '@/styles/auth-quiet.css'

defineProps<{
  /** 当前是第几步，从 1 开始。 */
  current: number
  total: number
  label: string
}>()
</script>

<template>
  <div class="auth-step-head">
    <span class="auth-step-label">{{ label }}</span>
    <span
      aria-hidden="true"
      class="auth-step-dots"
      :style="{ width: `${24 * (total - 1) + 18}px` }"
    >
      <span
        v-for="dot in total"
        :key="dot"
        class="auth-step-dot"
        :style="{ left: `${(dot - 1) * 24}px` }"
      ></span>
      <span
        class="auth-step-fill"
        :style="{ transform: `translateX(${(current - 1) * 24}px)` }"
      ></span>
    </span>
  </div>
</template>
