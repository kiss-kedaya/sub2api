<template>
  <BaseDialog :show="show" :title="t('admin.users.groupConfig')" width="wide" @close="$emit('close')">
    <div v-if="user" class="space-y-6">
      <div class="flex items-center gap-3">
        <UAvatar :alt="user.email" size="lg" />
        <div class="min-w-0 flex-1">
          <p class="truncate font-semibold text-highlighted">{{ user.email }}</p>
          <p class="mt-0.5 text-sm text-muted">{{ t('admin.users.groupConfigHint', { email: user.email }) }}</p>
        </div>
      </div>

      <div v-if="loading" class="flex justify-center py-12">
        <UIcon name="i-lucide-loader-circle" class="size-8 animate-spin text-dimmed" />
      </div>

      <div v-else class="space-y-6">
        <!-- Exclusive groups -->
        <section v-if="exclusiveGroups.length > 0">
          <p class="mb-2 text-sm font-medium text-highlighted">
            {{ t('admin.users.exclusiveGroups') }}
            <span class="ml-1 text-xs font-normal text-muted">({{ exclusiveGroupConfigs.filter(c => c.isSelected).length }}/{{ exclusiveGroupConfigs.length }})</span>
          </p>
          <ul class="divide-y divide-default border-y border-default">
            <li v-for="config in exclusiveGroupConfigs" :key="config.groupId" class="flex items-center gap-4 py-3">
              <UCheckbox :model-value="config.isSelected" :aria-label="config.groupName" @update:model-value="toggleExclusiveGroup(config.groupId)" />
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-2">
                  <span class="font-medium text-highlighted">{{ config.groupName }}</span>
                  <UBadge color="primary" variant="subtle" size="sm" :label="t('admin.groups.exclusive')" />
                </div>
                <p class="mt-1 flex items-center gap-2 text-xs text-muted">
                  <span class="inline-flex items-center gap-1"><PlatformIcon :platform="config.platform" size="xs" />{{ config.platform }}</span>
                  <span>·</span>
                  <span>{{ t('admin.users.defaultRate') }}: <span class="font-medium text-default">{{ config.defaultRate }}x</span></span>
                </p>
              </div>
              <UFormField :label="t('admin.users.customRate')" orientation="horizontal" class="shrink-0 items-center gap-2">
                <UInput
                  type="number"
                  step="0.001"
                  min="0.001"
                  :model-value="config.customRate == null ? '' : String(config.customRate)"
                  :placeholder="String(config.defaultRate)"
                  class="w-24"
                  :ui="{ base: noSpinner }"
                  @update:model-value="(value) => updateCustomRate(config.groupId, String(value ?? ''))"
                />
              </UFormField>
            </li>
          </ul>
        </section>

        <!-- Public groups -->
        <section v-if="publicGroups.length > 0">
          <div class="mb-1 flex flex-wrap items-center gap-2">
            <p class="text-sm font-medium text-highlighted">
              {{ restrictPublicGroups ? t('admin.users.publicGroupsRestricted') : t('admin.users.publicGroups') }}
              <span class="ml-1 text-xs font-normal text-muted">({{ publicGroupConfigs.length }})</span>
            </p>
            <USwitch class="ml-auto" :model-value="restrictPublicGroups" :label="t('admin.users.restrictPublicGroups')" @update:model-value="toggleRestrictPublicGroups" />
          </div>
          <p class="mb-2 text-xs text-muted">{{ t('admin.users.restrictPublicGroupsHint') }}</p>
          <ul class="divide-y divide-default border-y border-default">
            <li v-for="config in publicGroupConfigs" :key="config.groupId" class="flex items-center gap-4 py-3">
              <!-- 未开启限制时公开分组恒可用，此处仅作展示；开启后才是真实开关 -->
              <UCheckbox
                :model-value="restrictPublicGroups ? config.isSelected : true"
                :disabled="!restrictPublicGroups"
                :aria-label="config.groupName"
                @update:model-value="restrictPublicGroups && togglePublicGroup(config.groupId)"
              />
              <div class="min-w-0 flex-1">
                <span class="font-medium text-highlighted">{{ config.groupName }}</span>
                <p class="mt-1 flex items-center gap-2 text-xs text-muted">
                  <span class="inline-flex items-center gap-1"><PlatformIcon :platform="config.platform" size="xs" />{{ config.platform }}</span>
                  <span>·</span>
                  <span>{{ t('admin.users.defaultRate') }}: <span class="font-medium text-default">{{ config.defaultRate }}x</span></span>
                </p>
              </div>
              <UFormField :label="t('admin.users.customRate')" orientation="horizontal" class="shrink-0 items-center gap-2">
                <UInput
                  type="number"
                  step="0.001"
                  min="0.001"
                  :model-value="config.customRate == null ? '' : String(config.customRate)"
                  :placeholder="String(config.defaultRate)"
                  class="w-24"
                  :ui="{ base: noSpinner }"
                  @update:model-value="(value) => updateCustomRate(config.groupId, String(value ?? ''))"
                />
              </UFormField>
            </li>
          </ul>
        </section>

        <div v-if="groups.length === 0" class="flex flex-col items-center justify-center py-12 text-center">
          <UIcon name="i-lucide-layers" class="mb-3 size-10 text-dimmed" />
          <p class="text-muted">{{ t('common.noGroupsAvailable') }}</p>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="$emit('close')" />
        <UButton :loading="submitting" :label="submitting ? t('common.saving') : t('common.save')" @click="handleSave" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { fetchPaginatedItems } from '@/utils/paginatedItems'
import type { AdminUser, Group, GroupPlatform } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'

interface GroupRateConfig {
  groupId: number
  groupName: string
  platform: GroupPlatform
  isExclusive: boolean
  defaultRate: number
  customRate: number | null
  isSelected: boolean
}

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
const emit = defineEmits(['close', 'success'])
const { t } = useI18n()
const appStore = useAppStore()

const groups = ref<Group[]>([])
const groupConfigs = ref<GroupRateConfig[]>([])
const originalGroupRates = ref<Record<number, number>>({}) // 记录原始专属倍率，用于检测删除
const loading = ref(false)
const submitting = ref(false)
const restrictPublicGroups = ref(false)

// 分离专属分组和公开分组
const exclusiveGroups = computed(() => groups.value.filter((g) => g.is_exclusive))
const publicGroups = computed(() => groups.value.filter((g) => !g.is_exclusive))

const exclusiveGroupConfigs = computed(() => groupConfigs.value.filter((c) => c.isExclusive))
const publicGroupConfigs = computed(() => groupConfigs.value.filter((c) => !c.isExclusive))

watch(
  () => props.show,
  (v) => {
    if (v && props.user) {
      load()
    }
  }
)

const load = async () => {
  loading.value = true
  try {
    const allGroups = await fetchPaginatedItems(
      (page, size) => adminAPI.groups.list(page, size), 1000, 1000
    )
    // 只显示标准类型且活跃的分组
    groups.value = allGroups.filter((g) => g.subscription_type === 'standard' && g.status === 'active')

    // 初始化配置
    const userAllowedGroups = props.user?.allowed_groups || []
    const userGroupRates = props.user?.group_rates || {}
    restrictPublicGroups.value = props.user?.restrict_public_groups ?? false

    // 保存原始专属倍率，用于检测删除操作
    originalGroupRates.value = { ...userGroupRates }

    groupConfigs.value = groups.value.map((g) => ({
      groupId: g.id,
      groupName: g.name,
      platform: g.platform,
      isExclusive: g.is_exclusive,
      defaultRate: g.rate_multiplier,
      customRate: userGroupRates[g.id] ?? null,
      // 专属分组：检查是否在 allowed_groups 中
      // 公开分组：未开启限制时恒可用；开启后同样以 allowed_groups 为准
      isSelected:
        g.is_exclusive || restrictPublicGroups.value ? userAllowedGroups.includes(g.id) : true,
    }))
  } catch (error) {
    console.error('Failed to load groups:', error)
  } finally {
    loading.value = false
  }
}

// Rate inputs hide the number spinners (classes on the Nuxt UI input element)
const noSpinner = '[appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none'

const toggleExclusiveGroup = (groupId: number) => {
  const config = groupConfigs.value.find((c) => c.groupId === groupId)
  if (config && config.isExclusive) {
    config.isSelected = !config.isSelected
  }
}

const togglePublicGroup = (groupId: number) => {
  const config = groupConfigs.value.find((c) => c.groupId === groupId)
  if (config && !config.isExclusive) {
    config.isSelected = !config.isSelected
  }
}

// 关闭限制时把公开分组全部勾回，避免保存出一份"限制已关但只勾了两个"的误导状态。
const toggleRestrictPublicGroups = () => {
  restrictPublicGroups.value = !restrictPublicGroups.value
  if (!restrictPublicGroups.value) {
    for (const config of groupConfigs.value) {
      if (!config.isExclusive) config.isSelected = true
    }
  }
}

const updateCustomRate = (groupId: number, value: string) => {
  const config = groupConfigs.value.find((c) => c.groupId === groupId)
  if (config) {
    if (value === '' || value === null || value === undefined) {
      config.customRate = null
    } else {
      const numValue = parseFloat(value)
      config.customRate = isNaN(numValue) ? null : numValue
    }
  }
}

const handleSave = async () => {
  if (!props.user) return
  submitting.value = true

  try {
    // 构建 allowed_groups：专属分组中被勾选的，以及开启限制后被勾选的公开分组。
    // 未开启限制时不写入公开分组，保持该表"额外授予"的原有语义。
    const allowedGroups = groupConfigs.value
      .filter((c) => c.isSelected && (c.isExclusive || restrictPublicGroups.value))
      .map((c) => c.groupId)

    // 构建 group_rates
    // - 有新专属倍率: 设置为该值
    // - 原本有专属倍率但现在被清空: 设置为 null（表示删除）
    const groupRates: Record<number, number | null> = {}
    for (const c of groupConfigs.value) {
      const hadOriginalRate = originalGroupRates.value[c.groupId] !== undefined

      if (c.customRate !== null) {
        // 有专属倍率
        groupRates[c.groupId] = c.customRate
      } else if (hadOriginalRate) {
        // 原本有专属倍率，现在被清空，需要显式删除
        groupRates[c.groupId] = null
      }
    }

    await adminAPI.users.update(props.user.id, {
      allowed_groups: allowedGroups,
      restrict_public_groups: restrictPublicGroups.value,
      group_rates: Object.keys(groupRates).length > 0 ? groupRates : undefined,
    })

    appStore.showSuccess(t('admin.users.groupConfigUpdated'))
    emit('success')
    emit('close')
  } catch (error) {
    console.error('Failed to update user group config:', error)
  } finally {
    submitting.value = false
  }
}
</script>
