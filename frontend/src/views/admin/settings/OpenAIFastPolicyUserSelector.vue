<template>
  <div ref="containerRef" class="relative">
    <div v-if="selectedUserIds.length > 0" class="mb-2 flex flex-wrap gap-2">
      <UBadge
        v-for="userId in selectedUserIds"
        :key="userId"
        color="neutral"
        variant="subtle"
        class="max-w-full gap-1.5"
      >
        <span class="max-w-64 truncate font-medium" :title="selectedUserLabel(userId)">
          {{ selectedUserLabel(userId) }}
        </span>
        <span class="shrink-0 text-dimmed">#{{ userId }}</span>
        <span v-if="selectedUsers[userId]?.deleted" class="shrink-0 text-dimmed">
          {{ t("admin.settings.openaiFastPolicy.userDeleted") }}
        </span>
        <UButton
          icon="i-lucide-x"
          square
          :aria-label="t('admin.settings.openaiFastPolicy.removeUser')"
          :title="t('admin.settings.openaiFastPolicy.removeUser')"
          @click="removeUser(userId)"
          color="neutral"
          variant="link"
          size="xs"
          class="shrink-0 p-0"
        />
      </UBadge>
    </div>

    <div ref="anchorRef">
      <UInput
        icon="i-lucide-search"
        v-model="searchQuery"
        type="text"
        autocomplete="off"
        :placeholder="t('admin.settings.openaiFastPolicy.userSearchPlaceholder')"
        @input="debounceSearch"
        @focus="showDropdown = true"
        size="sm"
        class="w-full"
      />
    </div>

    <Teleport to="body">
      <div
        v-if="isDropdownOpen"
        ref="dropdownRef"
        :style="dropdownStyle"
        class="fixed z-50 overflow-auto rounded-xl bg-default p-1 ring ring-default shadow-lg"
      >
        <div v-if="searchLoading" class="px-4 py-3 text-sm text-muted">
          {{ t("common.loading") }}
        </div>
        <div v-else-if="availableResults.length === 0" class="px-4 py-3 text-sm text-muted">
          {{ t("admin.settings.openaiFastPolicy.userSearchEmpty") }}
        </div>
        <template v-else>
          <UButton
            v-for="user in availableResults"
            :key="user.id"
            @click="selectUser(user)"
            color="neutral"
            variant="ghost"
            class="w-full justify-between"
          >
            <span class="min-w-0 truncate font-medium text-highlighted">
              {{ user.email }}
              <span v-if="user.deleted" class="ml-1 text-xs font-normal text-dimmed">
                {{ t("admin.settings.openaiFastPolicy.userDeleted") }}
              </span>
            </span>
            <span class="shrink-0 text-xs tabular-nums text-dimmed">#{{ user.id }}</span>
          </UButton>
        </template>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch, type CSSProperties } from "vue";
import { useI18n } from "vue-i18n";
import { adminAPI } from "@/api/admin";
import type { SimpleUser } from "@/api/admin/usage";

const props = defineProps<{
  modelValue: number[];
}>();

const emit = defineEmits<{
  "update:modelValue": [value: number[]];
}>();

const { t } = useI18n();
const containerRef = ref<HTMLElement | null>(null);
const anchorRef = ref<HTMLElement | null>(null);
const dropdownRef = ref<HTMLElement | null>(null);
const dropdownStyle = ref<CSSProperties>({});
const searchQuery = ref("");
const searchResults = ref<SimpleUser[]>([]);
const searchLoading = ref(false);
const showDropdown = ref(false);
const selectedUsers = ref<Record<number, SimpleUser>>({});
let searchTimer: ReturnType<typeof setTimeout> | null = null;
let searchSequence = 0;
let positionListenersActive = false;

const isDropdownOpen = computed(() => showDropdown.value && searchQuery.value.trim().length > 0);

function updateDropdownPosition(): void {
  const anchor = anchorRef.value;
  if (!anchor) return;
  const rect = anchor.getBoundingClientRect();
  const margin = 8;
  const maxAllowedWidth = Math.max(200, window.innerWidth - margin * 2);
  const width = Math.min(Math.max(rect.width, 200), maxAllowedWidth);
  const left = Math.max(margin, Math.min(rect.left, window.innerWidth - width - margin));
  const spaceBelow = window.innerHeight - rect.bottom - margin - 4;
  const spaceAbove = rect.top - margin - 4;
  const openUpward = spaceBelow < 160 && spaceAbove > spaceBelow;
  const available = openUpward ? spaceAbove : spaceBelow;
  const maxHeight = Math.max(120, Math.min(240, available));

  if (openUpward) {
    dropdownStyle.value = {
      left: `${left}px`,
      width: `${width}px`,
      bottom: `${window.innerHeight - rect.top + 4}px`,
      maxHeight: `${maxHeight}px`,
      transformOrigin: "bottom center",
    };
  } else {
    dropdownStyle.value = {
      left: `${left}px`,
      width: `${width}px`,
      top: `${rect.bottom + 4}px`,
      maxHeight: `${maxHeight}px`,
      transformOrigin: "top center",
    };
  }
}

function addPositionListeners(): void {
  if (positionListenersActive) return;
  positionListenersActive = true;
  window.addEventListener("scroll", updateDropdownPosition, { capture: true, passive: true });
  window.addEventListener("resize", updateDropdownPosition);
}

function removePositionListeners(): void {
  if (!positionListenersActive) return;
  positionListenersActive = false;
  window.removeEventListener("scroll", updateDropdownPosition, { capture: true });
  window.removeEventListener("resize", updateDropdownPosition);
}

watch(isDropdownOpen, (open) => {
  if (open) {
    addPositionListeners();
    void nextTick(updateDropdownPosition);
  } else {
    removePositionListeners();
  }
});

const selectedUserIds = computed(() =>
  Array.from(new Set(props.modelValue.filter((id) => Number.isInteger(id) && id > 0))),
);

const availableResults = computed(() => {
  const selected = new Set(selectedUserIds.value);
  return searchResults.value
    .filter((user) => !selected.has(user.id))
    .sort((a, b) => Number(a.deleted) - Number(b.deleted));
});

function selectedUserLabel(userId: number): string {
  return selectedUsers.value[userId]?.email ||
    t("admin.settings.openaiFastPolicy.userIdFallback", { id: userId });
}

function clearPendingSearch(): void {
  if (searchTimer) {
    clearTimeout(searchTimer);
    searchTimer = null;
  }
  searchSequence += 1;
}

function debounceSearch(): void {
  clearPendingSearch();
  const query = searchQuery.value.trim();
  showDropdown.value = true;
  if (!query) {
    searchResults.value = [];
    searchLoading.value = false;
    return;
  }

  const sequence = searchSequence;
  searchTimer = setTimeout(async () => {
    searchLoading.value = true;
    try {
      const results = await adminAPI.usage.searchUsers(query);
      if (sequence === searchSequence) {
        searchResults.value = results;
      }
    } catch {
      if (sequence === searchSequence) {
        searchResults.value = [];
      }
    } finally {
      if (sequence === searchSequence) {
        searchLoading.value = false;
      }
    }
  }, 300);
}

function selectUser(user: SimpleUser): void {
  selectedUsers.value = { ...selectedUsers.value, [user.id]: user };
  emit("update:modelValue", [...selectedUserIds.value, user.id]);
  clearPendingSearch();
  searchQuery.value = "";
  searchResults.value = [];
  searchLoading.value = false;
  showDropdown.value = false;
}

function removeUser(userId: number): void {
  emit(
    "update:modelValue",
    selectedUserIds.value.filter((id) => id !== userId),
  );
}

async function hydrateSelectedUsers(userIds: number[]): Promise<void> {
  const missing = userIds.filter((id) => !selectedUsers.value[id]);
  if (missing.length === 0) return;

  const users = await Promise.all(
    missing.map(async (id) => {
      try {
        const user = await adminAPI.users.getById(id, true);
        return {
          id: user.id,
          email: user.email,
          deleted: Boolean(user.deleted_at),
        } satisfies SimpleUser;
      } catch {
        return null;
      }
    }),
  );

  const next = { ...selectedUsers.value };
  for (const user of users) {
    if (user && props.modelValue.includes(user.id)) {
      next[user.id] = user;
    }
  }
  selectedUsers.value = next;
}

function handleDocumentClick(event: MouseEvent): void {
  const target = event.target as Node | null;
  if (target && !containerRef.value?.contains(target) && !dropdownRef.value?.contains(target)) {
    showDropdown.value = false;
  }
}

watch(
  selectedUserIds,
  (userIds) => {
    void hydrateSelectedUsers(userIds);
  },
  { immediate: true },
);

onMounted(() => {
  document.addEventListener("click", handleDocumentClick);
});

onUnmounted(() => {
  clearPendingSearch();
  removePositionListeners();
  document.removeEventListener("click", handleDocumentClick);
});
</script>
