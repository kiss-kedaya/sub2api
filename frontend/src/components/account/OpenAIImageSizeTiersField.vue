<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import {
  OPENAI_IMAGE_SIZE_TIERS,
  normalizeOpenAIImageSizeTiers,
  type OpenAIImageSizeTier
} from './openaiImageSizeTiers'

const props = defineProps<{
  disabled?: boolean
  hideLabel?: boolean
  testIdPrefix?: string
}>()
const model = defineModel<OpenAIImageSizeTier[]>({ default: () => [...OPENAI_IMAGE_SIZE_TIERS] })
const { t } = useI18n()

const isLastSelected = (tier: OpenAIImageSizeTier) => model.value.length === 1 && model.value[0] === tier

// 至少保留一个档位：取消最后一个勾选时不动 model，控件保持选中。
// UCheckbox 的 update:model-value 在 group 模式下会带 value 字符串，这里只认布尔 true。
const toggleTier = (tier: OpenAIImageSizeTier, checked: boolean | string) => {
  const nextChecked = checked === true
  if (!nextChecked && isLastSelected(tier)) {
    return
  }
  const next = nextChecked ? [...model.value, tier] : model.value.filter(item => item !== tier)
  model.value = normalizeOpenAIImageSizeTiers(next)
}

const testId = (tier: OpenAIImageSizeTier) => `${props.testIdPrefix ?? 'openai-image-size-tier'}-${tier}`
</script>

<template>
  <div>
    <label v-if="!hideLabel" class="mb-2 block text-sm font-medium text-default">
      {{ t('admin.accounts.openai.imageSizeTiers') }}
    </label>
    <div class="flex flex-wrap items-center gap-4" role="group" :aria-label="t('admin.accounts.openai.imageSizeTiers')">
      <UCheckbox
        v-for="tier in OPENAI_IMAGE_SIZE_TIERS"
        :key="tier"
        :model-value="model.includes(tier)"
        :data-testid="testId(tier)"
        :label="tier"
        :disabled="disabled"
        :title="isLastSelected(tier) ? t('admin.accounts.openai.imageSizeTiersAtLeastOne') : undefined"
        @update:model-value="toggleTier(tier, $event)"
      />
    </div>
    <p v-if="!hideLabel" class="mt-1 text-xs text-muted">
      {{ t('admin.accounts.openai.imageSizeTiersDesc') }}
    </p>
  </div>
</template>
