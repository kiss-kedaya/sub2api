<template>
  <section class="space-y-4 border-t border-default pt-4">
    <UCheckbox
      :model-value="settings.enabled"
      label="MiniMax-H3"
      :description="copy.hint"
      data-testid="h3-enable"
      @update:model-value="update({ enabled: $event === true })"
    />
    <div v-if="settings.enabled" class="grid gap-4 sm:grid-cols-2">
      <UFormField :label="copy.mode">
        <USelect
          :model-value="settings.mode"
          :items="modeItems"
          class="w-full"
          data-testid="h3-mode"
          @update:model-value="update({ mode: String($event) })"
        />
      </UFormField>
      <UFormField v-if="settings.mode === 'per_request'" :label="copy.seconds">
        <USelect
          :model-value="settings.seconds"
          :items="secondsItems"
          class="w-full"
          data-testid="h3-seconds"
          @update:model-value="update({ seconds: Number($event) })"
        />
      </UFormField>
      <UFormField
        :label="settings.mode === 'per_second' ? copy.pricePerSecond : copy.pricePerRequest"
        :help="copy.billing"
        required
        class="sm:col-span-2"
      >
        <UInput
          type="number"
          min="0"
          step="any"
          required
          class="w-full sm:w-1/2"
          :model-value="String(settings.price ?? '')"
          data-testid="h3-price"
          @update:model-value="update({ price: $event })"
        />
      </UFormField>
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
const modeItems = computed(() => [
  { label: copy.value.perSecond, value: 'per_second' },
  { label: copy.value.perRequest, value: 'per_request' }
])
const secondsItems = Array.from({ length: 15 }, (_, i) => ({ label: String(i + 1), value: i + 1 }))
function update(change: Partial<ReturnType<typeof readMiniMaxH3Price>>) {
  emit('update:modelValue', writeMiniMaxH3Price(props.modelValue, { ...settings.value, ...change }))
}
</script>
