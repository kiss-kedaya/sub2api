<template>
  <section class="rounded-xl border border-gray-200 p-4 dark:border-dark-600">
    <label class="flex cursor-pointer items-start gap-3">
      <input type="checkbox" class="mt-1 h-4 w-4 rounded-sm text-primary-600" :checked="settings.enabled" data-testid="h3-enable" @change="update({ enabled: ($event.target as HTMLInputElement).checked })" />
      <span>
        <span class="block text-sm font-medium text-gray-900 dark:text-white">MiniMax-H3</span>
        <span class="mt-1 block text-xs leading-5 text-gray-500">{{ copy.hint }}</span>
      </span>
    </label>
    <div v-if="settings.enabled" class="mt-4 grid gap-4 sm:grid-cols-2">
      <label class="block text-sm">
        <span class="input-label">{{ copy.mode }}</span>
        <select class="input" :value="settings.mode" data-testid="h3-mode" @change="update({ mode: ($event.target as HTMLSelectElement).value })">
          <option value="per_second">{{ copy.perSecond }}</option>
          <option value="per_request">{{ copy.perRequest }}</option>
        </select>
      </label>
      <label class="block text-sm">
        <span class="input-label">{{ settings.mode === 'per_second' ? copy.pricePerSecond : copy.pricePerRequest }}</span>
        <input type="number" min="0" step="any" required class="input" :value="settings.price ?? ''" data-testid="h3-price" @input="update({ price: ($event.target as HTMLInputElement).value })" />
      </label>
      <label v-if="settings.mode === 'per_request'" class="block text-sm">
        <span class="input-label">{{ copy.seconds }}</span>
        <select class="input" :value="settings.seconds" data-testid="h3-seconds" @change="update({ seconds: Number(($event.target as HTMLSelectElement).value) })">
          <option v-for="seconds in 15" :key="seconds" :value="seconds">{{ seconds }}</option>
        </select>
      </label>
      <p class="text-xs leading-5 text-gray-500 sm:col-span-2">{{ copy.billing }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { readMiniMaxH3Price, writeMiniMaxH3Price, type MiniMaxH3Pricing } from './miniMaxH3Pricing'

const props = defineProps<{ modelValue: MiniMaxH3Pricing }>()
const emit = defineEmits<{ 'update:modelValue': [value: MiniMaxH3Pricing] }>()
const { locale } = useI18n()
const settings = computed(() => readMiniMaxH3Price(props.modelValue))
const copy = computed(() => locale.value.startsWith('zh') ? {
  hint: '仅用于支持 JSON /v1/videos 的 MiniMax-H3 中转上游，需配置非 Coding 账号与自定义服务地址。',
  mode: '计价方式', perSecond: '按请求生成秒数', perRequest: '按固定时长每次',
  pricePerSecond: '基础价（余额 / 秒）', pricePerRequest: '基础价（余额 / 次）', seconds: '每次固定生成秒数',
  billing: '沿用视频倍率，按实际选中的分组冻结价格。失败不收费，完成只结算一次；按次计费仅接受所选时长。'
} : {
  hint: 'For a MiniMax-H3 relay implementing JSON /v1/videos. Requires a non-Coding account and a custom base URL.',
  mode: 'Pricing mode', perSecond: 'Per requested second', perRequest: 'Per fixed-duration request',
  pricePerSecond: 'Base price (balance / second)', pricePerRequest: 'Base price (balance / request)', seconds: 'Fixed request duration (seconds)',
  billing: 'Uses the video multiplier and freezes the selected group price. Failed tasks do not charge; successful tasks settle once. Per-request pricing accepts only the selected duration.'
})
function update(change: Partial<ReturnType<typeof readMiniMaxH3Price>>) {
  emit('update:modelValue', writeMiniMaxH3Price(props.modelValue, { ...settings.value, ...change }))
}
</script>
