<template>
  <div class="space-y-5">
    <div v-for="field in fields" :key="field.tier" class="space-y-2">
      <UFormField
        :label="field.label"
        :description="field.hint"
        :error="field.errorText"
      >
        <UInput
          :id="field.inputId"
          :model-value="field.inputValue"
          :type="field.locked ? 'text' : 'number'"
          step="any"
          min="0.0001"
          :max="OPENAI_TIER_MULTIPLIER_MAX"
          :placeholder="field.placeholder"
          :disabled="field.locked"
          :data-testid="field.inputId"
          :ui="{ base: field.locked ? 'tabular-nums bg-elevated/50 text-muted' : 'tabular-nums' }"
          class="w-full"
          @update:model-value="setField(field.tier, $event)"
        />
      </UFormField>

      <div
        class="flex flex-wrap items-center gap-2"
        role="group"
        :aria-label="field.label"
      >
        <UButton
          v-for="chip in field.chips"
          :key="chip.key"
          type="button"
          size="xs"
          color="neutral"
          :variant="chip.active ? 'soft' : 'outline'"
          :icon="chip.active ? 'i-lucide-check' : undefined"
          :label="chip.label"
          :disabled="field.locked"
          :aria-label="chip.ariaLabel"
          :aria-pressed="chip.active"
          :data-testid="`${field.inputId}-${chip.key}`"
          class="transition-colors duration-150 ease-out motion-reduce:transition-none"
          @click="setField(field.tier, chip.value)"
        />
      </div>

      <p
        v-if="field.preview"
        :data-testid="`${field.inputId}-preview`"
        class="text-xs text-muted tabular-nums"
      >
        {{ field.preview }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
// Fast / UltraFast 档位倍率：叠在分组倍率上，留空 = 官方默认，填 1 = 不加价。
// 校验、换算都在 groupsOpenAIFast.ts（纯函数）；这里只管渲染和把值回写给表单。
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import {
  OPENAI_TIER_DEFAULT_MULTIPLIER,
  OPENAI_TIER_MULTIPLIER_MAX,
  formatTierMultiplier,
  openAITierMultiplierInputId,
  parseOpenAITierMultiplier,
  type OpenAITier,
  type OpenAITierMultiplierIssue,
} from "@/views/admin/groupsOpenAIFast";

type FieldValue = string | number;

const props = defineProps<{
  /** 创建 / 编辑弹窗各一份，用来区分 id。 */
  idPrefix: string;
  /** 表单里的分组倍率（已处理空值），只用于预览。 */
  groupRate: number;
  /** 免费 Fast 已开启：Fast 输入框锁成 1×（UltraFast 不受影响）。 */
  freeFast: boolean;
}>();
const fast = defineModel<FieldValue>("fast", { default: "" });
const ultrafast = defineModel<FieldValue>("ultrafast", { default: "" });
const { t } = useI18n();

const tiers: OpenAITier[] = ["fast", "ultrafast"];

function setField(tier: OpenAITier, value: FieldValue) {
  if (tier === "fast") fast.value = value;
  else ultrafast.value = value;
}

function issueText(issue: OpenAITierMultiplierIssue): string {
  if (issue === "tooLarge") {
    return t("admin.groups.openaiFast.tierMultiplier.errors.tooLarge");
  }
  if (issue === "tooManyDecimals") {
    return t("admin.groups.openaiFast.tierMultiplier.errors.tooManyDecimals");
  }
  return t("admin.groups.openaiFast.tierMultiplier.errors.nonPositive");
}

const fields = computed(() =>
  tiers.map((tier) => {
    const isFast = tier === "fast";
    const raw = isFast ? fast.value : ultrafast.value;
    const locked = isFast && props.freeFast;
    const parsed = parseOpenAITierMultiplier(raw);
    // 锁定时 Fast 的值不提交也不参与校验，别让一个看不见的旧值挡住保存。
    const issue = locked ? null : parsed.issue;
    const label = isFast
      ? t("admin.groups.openaiFast.tierMultiplier.fastLabel")
      : t("admin.groups.openaiFast.tierMultiplier.ultrafastLabel");
    const name = isFast ? "Fast" : "UltraFast";
    // null = 官方默认（留空）；锁定时按免费 1× 算。
    const effective = locked ? 1 : parsed.value;

    let preview: string | null = null;
    if (!issue) {
      const rate = formatTierMultiplier(props.groupRate);
      preview =
        effective === null
          ? t("admin.groups.openaiFast.tierMultiplier.previewOfficial", {
              name,
              rate,
            })
          : t("admin.groups.openaiFast.tierMultiplier.previewValue", {
              name,
              rate,
              multiplier: formatTierMultiplier(effective),
              total: formatTierMultiplier(props.groupRate * effective),
            });
    }

    return {
      tier,
      label,
      locked,
      inputId: openAITierMultiplierInputId(props.idPrefix, tier),
      hint: isFast
        ? t("admin.groups.openaiFast.tierMultiplier.fastHint")
        : t("admin.groups.openaiFast.tierMultiplier.ultrafastHint"),
      placeholder: t("admin.groups.openaiFast.tierMultiplier.placeholder", {
        value: OPENAI_TIER_DEFAULT_MULTIPLIER[tier],
      }),
      // 输入框的 DOM 值本来就是字符串；统一成 string，UInput 的值类型才推得稳。
      inputValue: locked
        ? t("admin.groups.openaiFast.tierMultiplier.lockedFree")
        : String(raw ?? ""),
      errorText: issue ? issueText(issue) : undefined,
      preview,
      chips: [
        {
          key: "default",
          value: "" as FieldValue,
          active: !locked && parsed.value === null && !issue,
          label: t("admin.groups.openaiFast.tierMultiplier.useDefault"),
          ariaLabel: t(
            "admin.groups.openaiFast.tierMultiplier.useDefaultAria",
            { name: label },
          ),
        },
        {
          key: "free",
          value: 1 as FieldValue,
          active: !locked && parsed.value === 1,
          label: t("admin.groups.openaiFast.tierMultiplier.freeOneX"),
          ariaLabel: t("admin.groups.openaiFast.tierMultiplier.freeOneXAria", {
            name: label,
          }),
        },
      ],
    };
  }),
);
</script>
