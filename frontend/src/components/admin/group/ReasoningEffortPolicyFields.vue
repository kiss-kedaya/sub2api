<template>
  <div class="space-y-4">
    <UFormField
      :label="t('admin.groups.form.maxReasoningEffort')"
      :help="t('admin.groups.form.maxReasoningEffortHint')"
    >
      <Select
        :id="`${idPrefix}-max-effort`"
        :model-value="maxEffort"
        :options="reasoningEffortOptions"
        :placeholder="t('admin.groups.form.maxReasoningEffortUnlimited')"
        :aria-label="t('admin.groups.form.maxReasoningEffort')"
        :searchable="false"
        clearable
        @update:model-value="updateMaxEffort"
      />
    </UFormField>

    <UFormField
      :label="t('admin.groups.form.maxReasoningEffortOverLimit')"
      :help="t('admin.groups.form.maxReasoningEffortOverLimitHint')"
    >
      <Select
        :id="`${idPrefix}-over-limit`"
        :model-value="overLimit"
        :options="overLimitOptions"
        :aria-label="t('admin.groups.form.maxReasoningEffortOverLimit')"
        :searchable="false"
        :disabled="!maxEffort"
        @update:model-value="updateOverLimit"
      />
    </UFormField>

    <div class="space-y-3">
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <p class="text-sm font-medium text-default">
            {{ t("admin.groups.form.reasoningEffortMappings") }}
          </p>
          <p class="text-sm text-muted">
            {{ t("admin.groups.form.reasoningEffortMappingsHint") }}
          </p>
        </div>
        <UButton
          icon="i-lucide-plus"
          color="neutral"
          variant="outline"
          class="shrink-0"
          :label="t('admin.groups.form.addReasoningEffortMapping')"
          @click="addGroup"
        />
      </div>

      <div v-if="mappings.length > 0" class="space-y-2">
        <div
          v-for="group in mappings"
          :key="group.id"
          class="space-y-3 rounded-lg bg-elevated/50 p-3"
        >
          <div
            class="grid grid-cols-1 items-start gap-3 md:grid-cols-[minmax(0,1fr)_1.25rem_minmax(0,1fr)_2.25rem]"
          >
            <UFormField
              :label="t('admin.groups.form.reasoningEffortMatchType')"
              :error="showValidation && !!groupErrors(group.id).match_type"
            >
              <Select
                :id="`${idPrefix}-${group.id}-match-type`"
                :model-value="group.match_type"
                :options="matchTypeOptions"
                :placeholder="t('admin.groups.form.reasoningEffortMatchTypePlaceholder')"
                :error="showValidation && !!groupErrors(group.id).match_type"
                :aria-label="t('admin.groups.form.reasoningEffortMatchType')"
                :searchable="false"
                clearable
                @update:model-value="updateGroup(group.id, 'match_type', $event)"
              />
              <p
                v-if="showValidation && groupErrors(group.id).match_type"
                class="mt-1 text-xs text-error"
                role="alert"
              >
                {{ mappingErrorText(groupErrors(group.id).match_type) }}
              </p>
            </UFormField>

            <div class="hidden md:block" aria-hidden="true" />

            <UFormField :label="t('admin.groups.form.reasoningEffortModel')">
              <UInput
                :id="`${idPrefix}-${group.id}-model`"
                :model-value="group.model"
                maxlength="200"
                autocomplete="off"
                class="w-full"
                :placeholder="t('admin.groups.form.reasoningEffortModelPlaceholder')"
                :aria-label="t('admin.groups.form.reasoningEffortModel')"
                @update:model-value="updateGroup(group.id, 'model', $event)"
              />
            </UFormField>

            <UButton
              icon="i-lucide-trash-2"
              color="neutral"
              variant="ghost"
              class="self-end justify-self-end text-muted hover:text-error md:mb-0.5"
              :title="t('admin.groups.form.removeReasoningEffortMapping')"
              :aria-label="t('admin.groups.form.removeReasoningEffortMapping')"
              @click="removeGroup(group.id)"
            />
          </div>

          <p
            v-if="showValidation && groupErrors(group.id).duplicateScope"
            class="text-xs text-error"
            role="alert"
          >
            {{ mappingErrorText(groupErrors(group.id).duplicateScope) }}
          </p>

          <div
            v-for="pair in group.pairs"
            :key="pair.id"
            class="grid grid-cols-1 items-start gap-3 md:grid-cols-[minmax(0,1fr)_1.25rem_minmax(0,1fr)_2.25rem]"
          >
            <UFormField
              :label="t('admin.groups.form.reasoningEffortFrom')"
              :error="showValidation && !!pairErrors(pair.id).from"
            >
              <Select
                :id="`${idPrefix}-${pair.id}-from`"
                :model-value="pair.from"
                :options="reasoningEffortSourceOptions"
                :placeholder="t('admin.groups.form.reasoningEffortFromPlaceholder')"
                :error="showValidation && !!pairErrors(pair.id).from"
                :aria-label="t('admin.groups.form.reasoningEffortFrom')"
                :searchable="false"
                clearable
                @update:model-value="updatePair(group.id, pair.id, 'from', $event)"
              />
              <p
                v-if="showValidation && pairErrors(pair.id).from"
                class="mt-1 text-xs text-error"
                role="alert"
              >
                {{ mappingErrorText(pairErrors(pair.id).from) }}
              </p>
            </UFormField>

            <div class="hidden h-9 items-center justify-center self-end text-dimmed md:flex" aria-hidden="true">
              <UIcon name="i-lucide-arrow-right" class="size-4" />
            </div>

            <UFormField
              :label="t('admin.groups.form.reasoningEffortTo')"
              :error="showValidation && !!pairErrors(pair.id).to"
            >
              <Select
                :id="`${idPrefix}-${pair.id}-to`"
                :model-value="pair.to"
                :options="reasoningEffortTargetOptions"
                :placeholder="t('admin.groups.form.reasoningEffortToPlaceholder')"
                :error="showValidation && !!pairErrors(pair.id).to"
                :aria-label="t('admin.groups.form.reasoningEffortTo')"
                :searchable="false"
                clearable
                @update:model-value="updatePair(group.id, pair.id, 'to', $event)"
              />
              <p
                v-if="showValidation && pairErrors(pair.id).to"
                class="mt-1 text-xs text-error"
                role="alert"
              >
                {{ mappingErrorText(pairErrors(pair.id).to) }}
              </p>
            </UFormField>

            <UButton
              icon="i-lucide-trash-2"
              color="neutral"
              variant="ghost"
              class="self-end justify-self-end text-muted hover:text-error md:mb-0.5"
              :title="t('admin.groups.form.removeReasoningEffortPair')"
              :aria-label="t('admin.groups.form.removeReasoningEffortPair')"
              @click="removePair(group.id, pair.id)"
            />
          </div>

          <UButton
            icon="i-lucide-plus"
            variant="link"
            class="px-0"
            :label="t('admin.groups.form.addReasoningEffortPair')"
            @click="addPair(group.id)"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import type { GroupPlatform } from "@/types";
import Select from "@/components/common/Select.vue";
import {
  createReasoningEffortMappingPair,
  createReasoningEffortMappingRow,
  normalizeReasoningEffortMatchType,
  reasoningEffortOptionsForPlatform,
  reasoningEffortSourceOptionsForPlatform,
  reasoningEffortTargetOptionsForPlatform,
  reasoningEffortMappingDeny,
  reasoningEffortOverLimitDeny,
  reasoningEffortOverLimitDowngrade,
  validateReasoningEffortMappings,
  type ReasoningEffortMappingErrorCode,
  type ReasoningEffortMappingRow,
} from "@/views/admin/groupsReasoningEffort";

const props = defineProps<{
  idPrefix: string;
  platform: GroupPlatform;
  maxEffort: string;
  overLimit: string;
  mappings: ReasoningEffortMappingRow[];
}>();

const emit = defineEmits<{
  (event: "update:maxEffort", value: string): void;
  (event: "update:overLimit", value: string): void;
  (event: "update:mappings", value: ReasoningEffortMappingRow[]): void;
}>();

const { t } = useI18n();
const showValidation = ref(false);
const reasoningEffortOptions = computed(() =>
  reasoningEffortOptionsForPlatform(props.platform),
);
const reasoningEffortSourceOptions = computed(() =>
  reasoningEffortSourceOptionsForPlatform(props.platform),
);
const reasoningEffortTargetOptions = computed(() =>
  reasoningEffortTargetOptionsForPlatform(props.platform).map((option) =>
    option.value === reasoningEffortMappingDeny
      ? {
          ...option,
          label: t("admin.groups.form.reasoningEffortToDeny"),
        }
      : option,
  ),
);
const matchTypeOptions = computed(() => [
  {
    value: "exact",
    label: t("admin.groups.form.reasoningEffortMatchExact"),
  },
  {
    value: "prefix",
    label: t("admin.groups.form.reasoningEffortMatchPrefix"),
  },
  {
    value: "suffix",
    label: t("admin.groups.form.reasoningEffortMatchSuffix"),
  },
]);
const overLimitOptions = computed(() => [
  {
    value: reasoningEffortOverLimitDowngrade,
    label: t("admin.groups.form.maxReasoningEffortOverLimitDowngrade"),
  },
  {
    value: reasoningEffortOverLimitDeny,
    label: t("admin.groups.form.maxReasoningEffortOverLimitDeny"),
  },
]);
const validationErrors = computed(() =>
  validateReasoningEffortMappings(props.mappings, props.platform),
);

const asString = (value: string | number | boolean | null): string =>
  value == null ? "" : String(value);

const groupErrors = (id: string) => validationErrors.value[id] ?? {};
const pairErrors = (id: string) => validationErrors.value[id] ?? {};

const updateMaxEffort = (value: string | number | boolean | null) => {
  emit("update:maxEffort", asString(value));
};

const updateOverLimit = (value: string | number | boolean | null) => {
  emit(
    "update:overLimit",
    asString(value) || reasoningEffortOverLimitDowngrade,
  );
};

const updateGroup = (
  id: string,
  field: "match_type" | "model",
  value: string | number | boolean | null,
) => {
  const nextValue =
    field === "match_type"
      ? normalizeReasoningEffortMatchType(asString(value))
      : asString(value);
  emit(
    "update:mappings",
    props.mappings.map((group) =>
      group.id === id ? { ...group, [field]: nextValue } : group,
    ),
  );
};

const updatePair = (
  groupId: string,
  pairId: string,
  field: "from" | "to",
  value: string | number | boolean | null,
) => {
  emit(
    "update:mappings",
    props.mappings.map((group) =>
      group.id === groupId
        ? {
            ...group,
            pairs: group.pairs.map((pair) =>
              pair.id === pairId ? { ...pair, [field]: asString(value) } : pair,
            ),
          }
        : group,
    ),
  );
};

const addGroup = () => {
  emit("update:mappings", [
    ...props.mappings,
    createReasoningEffortMappingRow(),
  ]);
};

const removeGroup = (id: string) => {
  emit(
    "update:mappings",
    props.mappings.filter((group) => group.id !== id),
  );
};

const addPair = (groupId: string) => {
  emit(
    "update:mappings",
    props.mappings.map((group) =>
      group.id === groupId
        ? { ...group, pairs: [...group.pairs, createReasoningEffortMappingPair()] }
        : group,
    ),
  );
};

const removePair = (groupId: string, pairId: string) => {
  emit(
    "update:mappings",
    props.mappings.flatMap((group) => {
      if (group.id !== groupId) return [group];
      const pairs = group.pairs.filter((pair) => pair.id !== pairId);
      return pairs.length > 0 ? [{ ...group, pairs }] : [];
    }),
  );
};

const mappingErrorText = (
  code: ReasoningEffortMappingErrorCode | undefined,
): string => (code ? t(`admin.groups.form.${code}`) : "");

const validate = (): boolean => {
  showValidation.value = true;
  return Object.keys(validationErrors.value).length === 0;
};

const resetValidation = () => {
  showValidation.value = false;
};

defineExpose({ validate, resetValidation });
</script>
