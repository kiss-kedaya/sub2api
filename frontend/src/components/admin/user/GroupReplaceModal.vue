<template>
  <BaseDialog :show="show" :title="t('admin.users.replaceGroupTitle')" width="narrow" @close="$emit('close')">
    <div v-if="oldGroup" class="space-y-4">
      <p class="text-sm text-muted">{{ t('admin.users.replaceGroupHint', { old: oldGroup.name }) }}</p>

      <!-- Current group → picked group -->
      <p class="flex items-center gap-2 text-sm">
        <UIcon name="i-lucide-shield" class="size-4 text-muted" />
        <span class="font-medium text-highlighted">{{ oldGroup.name }}</span>
        <UIcon name="i-lucide-arrow-right" class="size-4 text-dimmed" />
        <span v-if="selectedGroupId" class="font-medium text-primary">{{ availableGroups.find(g => g.id === selectedGroupId)?.name }}</span>
        <span v-else class="text-dimmed">?</span>
      </p>

      <URadioGroup
        v-if="availableGroups.length > 0"
        :model-value="selectedGroupId ?? undefined"
        @update:model-value="(value: unknown) => (selectedGroupId = value == null ? null : Number(value))"
        :items="groupItems"
        variant="card"
        class="max-h-64 overflow-y-auto"
      />
      <p v-else class="py-6 text-center text-sm text-muted">{{ t('admin.users.noOtherGroups') }}</p>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="$emit('close')" />
        <UButton
          :loading="submitting"
          :disabled="!selectedGroupId || submitting"
          :label="submitting ? t('common.saving') : t('admin.users.replaceGroupConfirm')"
          @click="handleReplace"
        />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { AdminUser, AdminGroup } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'

interface Props {
  show: boolean
  user: AdminUser | null
  oldGroup: { id: number; name: string } | null
  allGroups: AdminGroup[]
}

const props = defineProps<Props>()
const emit = defineEmits(['close', 'success'])
const { t } = useI18n()
const appStore = useAppStore()

const selectedGroupId = ref<number | null>(null)
const submitting = ref(false)

// 可选的专属标准分组（排除当前 oldGroup）
const availableGroups = computed(() => {
  if (!props.oldGroup) return []
  return props.allGroups.filter(
    g => g.status === 'active' && g.is_exclusive && g.subscription_type === 'standard' && g.id !== props.oldGroup!.id
  )
})

// Choices for the radio cards: group name, platform as the description
const groupItems = computed(() =>
  availableGroups.value.map((g) => ({ label: g.name, description: g.platform, value: g.id }))
)

watch(() => props.show, (v) => {
  if (v) {
    selectedGroupId.value = null
  }
})

const handleReplace = async () => {
  if (!props.user || !props.oldGroup || !selectedGroupId.value) return
  submitting.value = true

  try {
    const result = await adminAPI.users.replaceGroup(props.user.id, props.oldGroup.id, selectedGroupId.value)
    appStore.showSuccess(t('admin.users.replaceGroupSuccess', { count: result.migrated_keys }))
    emit('success')
    emit('close')
  } catch (error) {
    console.error('Failed to replace group:', error)
  } finally {
    submitting.value = false
  }
}
</script>
