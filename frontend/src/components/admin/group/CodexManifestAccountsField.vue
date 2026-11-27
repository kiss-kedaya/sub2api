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
          <Teleport :to="resolvedTeleportTarget" :disabled="teleportDisabled">
            <div
              v-if="showDropdown && (searchResults.length > 0 || searchKeyword.trim() !== '')"
              ref="dropdownRef"
              class="fixed z-50 max-h-48 overflow-auto rounded-xl bg-default p-1 shadow-lg ring ring-default"
              :style="dropdownStyle"
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
                <span class="text-xs tabular-nums text-dimmed">#{{ account.id }}</span>
              </UButton>
            </div>
          </Teleport>
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
import { computed, inject, nextTick, onMounted, onUnmounted, ref } from "vue";
import { portalTargetInjectionKey } from "@nuxt/ui/composables/usePortal";
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
const dropdownRef = ref<HTMLElement | null>(null);
const dropdownStyle = ref<Record<string, string>>({});

const portalTarget = inject(portalTargetInjectionKey, null);
const clippingAncestor = ref(false);

const hasClippingAncestor = (el: HTMLElement | null): boolean => {
  if (typeof window === "undefined") return false;
  let node = el?.parentElement ?? null;
  while (node && node !== document.body && node !== document.documentElement) {
    const style = window.getComputedStyle(node);
    const ov = `${style.overflow} ${style.overflowX} ${style.overflowY}`;
    if (/\b(hidden|auto|scroll|clip)\b/.test(ov)) return true;
    if (
      (style.transform && style.transform !== "none") ||
      (style.filter && style.filter !== "none") ||
      (style.backdropFilter && style.backdropFilter !== "none") ||
      /\b(layout|paint|strict|content)\b/.test(style.contain || "")
    ) {
      return true;
    }
    node = node.parentElement;
  }
  return false;
};

const resolvedTeleportTarget = computed<string | HTMLElement>(() => {
  const val = portalTarget?.value;
  return val && typeof val !== "boolean" ? val : "body";
});

const teleportDisabled = computed(() => {
  const val = portalTarget?.value;
  const hasPortalTarget = typeof val === "boolean" ? val : Boolean(val);
  return !hasPortalTarget && !clippingAncestor.value;
});

const MAX_DROPDOWN_HEIGHT = 192; // max-h-48

const updateDropdownPosition = () => {
  const rect = searchContainerRef.value?.getBoundingClientRect();
  if (!rect) return;
  const padding = 8;
  const gap = 4;
  const width = Math.min(Math.max(rect.width, 200), window.innerWidth - padding * 2);
  const left = Math.min(
    Math.max(rect.left, padding),
    Math.max(padding, window.innerWidth - width - padding),
  );
  const measuredHeight = dropdownRef.value?.offsetHeight || MAX_DROPDOWN_HEIGHT;
  const spaceBelow = window.innerHeight - padding - (rect.bottom + gap);
  const spaceAbove = rect.top - gap - padding;
  const flipAbove = spaceBelow < measuredHeight && spaceAbove > spaceBelow;
  if (flipAbove) {
    const maxHeight = Math.max(80, Math.min(MAX_DROPDOWN_HEIGHT, spaceAbove));
    dropdownStyle.value = {
      top: "auto",
      bottom: `${window.innerHeight - (rect.top - gap)}px`,
      left: `${left}px`,
      width: `${width}px`,
      maxHeight: `${maxHeight}px`,
      transformOrigin: "bottom left",
    };
  } else {
    const maxHeight = Math.max(80, Math.min(MAX_DROPDOWN_HEIGHT, spaceBelow));
    dropdownStyle.value = {
      top: `${Math.max(padding, rect.bottom + gap)}px`,
      bottom: "auto",
      left: `${left}px`,
      width: `${width}px`,
      maxHeight: `${maxHeight}px`,
      transformOrigin: "top left",
    };
  }
};

// 点击下拉容器外部时收起（支持 Teleport 到 body 或门户宿主的浮层）。
const handleDocumentClick = (event: MouseEvent) => {
  const target = event.target as HTMLElement;
  if (!searchContainerRef.value?.contains(target) && !dropdownRef.value?.contains(target)) {
    showDropdown.value = false;
  }
};
onMounted(() => {
  clippingAncestor.value = hasClippingAncestor(searchContainerRef.value);
  document.addEventListener("click", handleDocumentClick);
  window.addEventListener("resize", updateDropdownPosition);
  window.addEventListener("scroll", updateDropdownPosition, true);
});
onUnmounted(() => {
  document.removeEventListener("click", handleDocumentClick);
  window.removeEventListener("resize", updateDropdownPosition);
  window.removeEventListener("scroll", updateDropdownPosition, true);
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
    if (showDropdown.value) {
      void nextTick(updateDropdownPosition);
    }
  },
  onError: () => {
    searchResults.value = [];
  },
});

const searchAccounts = () => {
  searchRunner.trigger("codex-manifest", searchKeyword.value);
};

const onSearchFocus = () => {
  clippingAncestor.value = hasClippingAncestor(searchContainerRef.value);
  showDropdown.value = true;
  void nextTick(updateDropdownPosition);
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
