<template>
  <div class="mt-4 space-y-4 border-t border-default pt-4">
    <UFormField
      :label="t('admin.groups.codexModelsManifest.title')"
      :description="t('admin.groups.codexModelsManifest.hint')"
      orientation="horizontal"
      class="items-start gap-4"
    >
      <Toggle
        data-testid="codex-manifest-toggle"
        :aria-label="t('admin.groups.codexModelsManifest.enable')"
        :model-value="config.enabled"
        @update:model-value="emitUpdate({ enabled: $event })"
      />
    </UFormField>

    <div v-if="config.enabled" class="space-y-4">
      <UFormField
        :label="t('admin.groups.codexModelsManifest.accounts')"
        :description="t('admin.groups.codexModelsManifest.enabledHint')"
      >
        <!-- 已选账号标签 -->
        <div
          v-if="config.account_ids.length > 0"
          class="mb-2 flex flex-wrap gap-1.5"
          data-testid="codex-manifest-selected-tags"
        >
          <UBadge
            v-for="id in config.account_ids"
            :key="id"
            color="neutral"
            variant="subtle"
            class="gap-1 pe-1"
          >
            {{ accountLabel(id) }}
            <UButton
              icon="i-lucide-x"
              color="neutral"
              variant="link"
              size="xs"
              class="p-0"
              :aria-label="`remove account ${id}`"
              @click="removeAccount(id)"
            />
          </UBadge>
        </div>

        <!-- 搜索输入 + 下拉 -->
        <div class="relative" ref="searchContainerRef">
          <UInput
            v-model="searchKeyword"
            icon="i-lucide-search"
            class="w-full"
            data-testid="codex-manifest-search"
            :placeholder="t('admin.groups.codexModelsManifest.searchPlaceholder')"
            @update:model-value="searchAccounts"
            @focus="onSearchFocus"
          />
          <div
            v-if="showDropdown && (searchResults.length > 0 || searchKeyword.trim() !== '')"
            class="absolute z-50 mt-1 max-h-48 w-full overflow-auto rounded-md bg-default p-1 shadow-lg ring ring-default"
            data-testid="codex-manifest-dropdown"
          >
            <p
              v-if="searchResults.length === 0"
              class="px-2 py-1.5 text-sm text-muted"
              data-testid="codex-manifest-search-empty"
            >
              {{ t("admin.groups.codexModelsManifest.searchEmpty") }}
            </p>
            <UButton
              v-for="account in searchResults"
              :key="account.id"
              color="neutral"
              variant="ghost"
              block
              class="justify-start font-normal"
              :disabled="config.account_ids.includes(account.id)"
              @click="selectAccount(account)"
            >
              <span class="truncate">{{ account.name }}</span>
              <span class="text-xs text-dimmed">#{{ account.id }}</span>
            </UButton>
          </div>
        </div>
      </UFormField>

      <!-- 回退子开关 -->
      <UFormField
        :label="t('admin.groups.codexModelsManifest.fallback')"
        :description="t('admin.groups.codexModelsManifest.fallbackHint')"
        orientation="horizontal"
        class="items-start gap-4"
      >
        <Toggle
          data-testid="codex-manifest-fallback-toggle"
          :aria-label="t('admin.groups.codexModelsManifest.fallback')"
          :model-value="config.fallback_to_scheduler"
          @update:model-value="emitUpdate({ fallback_to_scheduler: $event })"
        />
      </UFormField>

      <p
        v-if="showValidationError"
        class="text-xs text-error"
        role="alert"
        data-testid="codex-manifest-validation-error"
      >
        {{ t("admin.groups.codexModelsManifest.selectAtLeastOne") }}
      </p>
    </div>
    <p v-else class="text-sm text-muted">
      {{ t("admin.groups.codexModelsManifest.disabledHint") }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import Toggle from "@/components/common/Toggle.vue";
import { useKeyedDebouncedSearch } from "@/composables/useKeyedDebouncedSearch";
import { adminAPI } from "@/api/admin";
import type { CodexModelsManifestConfig } from "@/types";

interface SimpleAccount {
  id: number;
  name: string;
}

const props = defineProps<{
  groupId: number;
  modelValue: CodexModelsManifestConfig;
  /** 已选账号 ID → 名称映射；无法解析的 ID 以 #<id> 展示 */
  accountNames?: Record<number, string>;
}>();

const emit = defineEmits<{
  (event: "update:modelValue", value: CodexModelsManifestConfig): void;
}>();

const { t } = useI18n();

// 本次会话内新选账号的名称缓存：props.accountNames 只覆盖打开对话框时已存的
// ID，搜索新选的账号名称由组件自己记住，避免标签闪现 #<id>。
const localNames = ref<Record<number, string>>({});

const config = computed(() => props.modelValue);

const emitUpdate = (patch: Partial<CodexModelsManifestConfig>) => {
  emit("update:modelValue", { ...props.modelValue, ...patch });
};

const accountLabel = (id: number) =>
  props.accountNames?.[id] ?? localNames.value[id] ?? `#${id}`;

const searchKeyword = ref("");
const searchResults = ref<SimpleAccount[]>([]);
const showDropdown = ref(false);
const showValidationError = ref(false);
const searchContainerRef = ref<HTMLElement | null>(null);

// 点击下拉容器外部时收起（与 GroupsView 模型路由下拉同一模式）。
const handleDocumentClick = (event: MouseEvent) => {
  const target = event.target as HTMLElement;
  if (searchContainerRef.value && !searchContainerRef.value.contains(target)) {
    showDropdown.value = false;
  }
};
onMounted(() => {
  document.addEventListener("click", handleDocumentClick);
});
onUnmounted(() => {
  document.removeEventListener("click", handleDocumentClick);
});

const searchRunner = useKeyedDebouncedSearch<SimpleAccount[]>({
  delay: 300,
  search: async (keyword, { signal }) => {
    const res = await adminAPI.accounts.list(
      1,
      20,
      {
        search: keyword,
        platform: "openai",
        group: String(props.groupId),
      },
      { signal },
    );
    return res.items.map((account) => ({
      id: account.id,
      name: account.name,
    }));
  },
  onSuccess: (_key, result) => {
    searchResults.value = result;
  },
  onError: () => {
    searchResults.value = [];
  },
});

const searchAccounts = () => {
  searchRunner.trigger("codex-manifest", searchKeyword.value);
};

const onSearchFocus = () => {
  showDropdown.value = true;
  if (searchResults.value.length === 0) {
    searchAccounts();
  }
};

const selectAccount = (account: SimpleAccount) => {
  if (props.modelValue.account_ids.includes(account.id)) return;
  localNames.value[account.id] = account.name;
  emitUpdate({
    account_ids: [...props.modelValue.account_ids, account.id],
  });
  searchKeyword.value = "";
  showDropdown.value = false;
  showValidationError.value = false;
};

const removeAccount = (id: number) => {
  emitUpdate({
    account_ids: props.modelValue.account_ids.filter(
      (accountId) => accountId !== id,
    ),
  });
};

/** 校验：开启后至少一个账号。返回是否通过；不通过时展示错误提示。 */
const validate = (): boolean => {
  if (props.modelValue.enabled && props.modelValue.account_ids.length === 0) {
    showValidationError.value = true;
    return false;
  }
  showValidationError.value = false;
  return true;
};

const resetValidation = () => {
  showValidationError.value = false;
};

defineExpose({ validate, resetValidation });
</script>
