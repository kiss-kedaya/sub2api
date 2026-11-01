<template>
  <div class="space-y-4">
    <UFormField :label="t('admin.announcements.form.targetingMode')">
      <URadioGroup
        :model-value="mode"
        :items="modeItems"
        orientation="horizontal"
        @update:model-value="(value: unknown) => setMode(value === 'custom' ? 'custom' : 'all')"
      />
    </UFormField>

    <div v-if="mode === 'custom'" class="space-y-4">
      <div class="flex items-center justify-between gap-2">
        <p class="text-sm font-medium text-highlighted">
          OR <span class="ml-1 text-xs font-normal text-muted">({{ anyOf.length }}/50)</span>
        </p>
        <UButton
          color="neutral"
          variant="outline"
          size="sm"
          icon="i-lucide-plus"
          :disabled="anyOf.length >= 50"
          :label="t('admin.announcements.form.addOrGroup')"
          @click="addOrGroup"
        />
      </div>

      <p v-if="anyOf.length === 0" class="text-sm text-muted">
        {{ t('admin.announcements.form.targetingCustom') }}: {{ t('admin.announcements.form.addOrGroup') }}
      </p>

      <template v-for="(group, groupIndex) in anyOf" :key="groupIndex">
        <USeparator v-if="groupIndex > 0" label="OR" />
        <div class="space-y-3">
          <div class="flex items-center justify-between gap-3">
            <p class="text-sm font-medium text-highlighted">
              {{ t('admin.announcements.form.targetingCustom') }} #{{ groupIndex + 1 }}
              <span class="ml-2 text-xs font-normal text-muted">AND ({{ (group.all_of?.length || 0) }}/50)</span>
            </p>
            <UButton
              color="error"
              variant="ghost"
              size="sm"
              icon="i-lucide-trash"
              :label="t('common.delete')"
              @click="removeOrGroup(groupIndex)"
            />
          </div>

          <div
            v-for="(cond, condIndex) in (group.all_of || [])"
            :key="condIndex"
            class="flex flex-col gap-3 md:flex-row md:items-end"
          >
            <UFormField :label="t('admin.announcements.form.conditionType')" class="w-full md:w-52">
              <Select
                :model-value="cond.type"
                :options="conditionTypeOptions"
                @update:model-value="(v) => setConditionType(groupIndex, condIndex, v as any)"
              />
            </UFormField>
            <UFormField v-if="cond.type === 'subscription'" :label="t('admin.announcements.form.selectPackages')" class="flex-1">
              <GroupSelector v-model="subscriptionSelections[groupIndex][condIndex]" :groups="groups" />
            </UFormField>
            <template v-else>
              <UFormField :label="t('admin.announcements.form.operator')" class="w-full sm:w-44">
                <Select
                  :model-value="cond.operator"
                  :options="balanceOperatorOptions"
                  @update:model-value="(v) => setOperator(groupIndex, condIndex, v as any)"
                />
              </UFormField>
              <UFormField :label="t('admin.announcements.form.balanceValue')" class="w-full sm:flex-1">
                <UInput
                  :model-value="String(cond.value ?? '')"
                  type="number"
                  step="any"
                  class="w-full"
                  @update:model-value="(value: string | number | undefined) => setBalanceValue(groupIndex, condIndex, String(value ?? ''))"
                />
              </UFormField>
            </template>
            <UButton
              color="neutral"
              variant="ghost"
              icon="i-lucide-x"
              square
              class="self-end"
              :aria-label="t('common.delete')"
              :title="t('common.delete')"
              @click="removeAndCondition(groupIndex, condIndex)"
            />
          </div>

          <UButton
            color="neutral"
            variant="link"
            size="sm"
            icon="i-lucide-plus"
            class="px-0"
            :disabled="(group.all_of?.length || 0) >= 50"
            :label="t('admin.announcements.form.addAndCondition')"
            @click="addAndCondition(groupIndex)"
          />
        </div>
      </template>

      <UAlert v-if="validationError" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="validationError" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  AdminGroup,
  AnnouncementTargeting,
  AnnouncementCondition,
  AnnouncementConditionGroup,
  AnnouncementConditionType,
  AnnouncementOperator
} from '@/types'

import Select from '@/components/common/Select.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'

const { t } = useI18n()

const props = defineProps<{
  modelValue: AnnouncementTargeting
  groups: AdminGroup[]
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: AnnouncementTargeting): void
}>()

const anyOf = computed(() => props.modelValue?.any_of ?? [])

type Mode = 'all' | 'custom'
const mode = computed<Mode>(() => (anyOf.value.length === 0 ? 'all' : 'custom'))

const conditionTypeOptions = computed(() => [
  { value: 'subscription', label: t('admin.announcements.form.conditionSubscription') },
  { value: 'balance', label: t('admin.announcements.form.conditionBalance') }
])

const balanceOperatorOptions = computed(() => [
  { value: 'gt', label: t('admin.announcements.operators.gt') },
  { value: 'gte', label: t('admin.announcements.operators.gte') },
  { value: 'lt', label: t('admin.announcements.operators.lt') },
  { value: 'lte', label: t('admin.announcements.operators.lte') },
  { value: 'eq', label: t('admin.announcements.operators.eq') }
])

const modeItems = computed(() => [
  { label: t('admin.announcements.form.targetingAll'), value: 'all' },
  { label: t('admin.announcements.form.targetingCustom'), value: 'custom' }
])

function setMode(next: Mode) {
  if (next === 'all') {
    emit('update:modelValue', { any_of: [] })
    return
  }
  if (anyOf.value.length === 0) {
    emit('update:modelValue', { any_of: [{ all_of: [defaultSubscriptionCondition()] }] })
  }
}

function defaultSubscriptionCondition(): AnnouncementCondition {
  return {
    type: 'subscription' as AnnouncementConditionType,
    operator: 'in' as AnnouncementOperator,
    group_ids: []
  }
}

function defaultBalanceCondition(): AnnouncementCondition {
  return {
    type: 'balance' as AnnouncementConditionType,
    operator: 'gte' as AnnouncementOperator,
    value: 0
  }
}

type TargetingDraft = {
  any_of: AnnouncementConditionGroup[]
}

function updateTargeting(mutator: (draft: TargetingDraft) => void) {
  const draft: TargetingDraft = JSON.parse(JSON.stringify(props.modelValue ?? { any_of: [] }))
  if (!draft.any_of) draft.any_of = []
  mutator(draft)
  emit('update:modelValue', draft)
}

function addOrGroup() {
  updateTargeting((draft) => {
    if (draft.any_of.length >= 50) return
    draft.any_of.push({ all_of: [defaultSubscriptionCondition()] })
  })
}

function removeOrGroup(groupIndex: number) {
  updateTargeting((draft) => {
    draft.any_of.splice(groupIndex, 1)
  })
}

function addAndCondition(groupIndex: number) {
  updateTargeting((draft) => {
    const group = draft.any_of[groupIndex]
    if (!group.all_of) group.all_of = []
    if (group.all_of.length >= 50) return
    group.all_of.push(defaultSubscriptionCondition())
  })
}

function removeAndCondition(groupIndex: number, condIndex: number) {
  updateTargeting((draft) => {
    const group = draft.any_of[groupIndex]
    if (!group?.all_of) return
    group.all_of.splice(condIndex, 1)
  })
}

function setConditionType(groupIndex: number, condIndex: number, nextType: AnnouncementConditionType) {
  updateTargeting((draft) => {
    const group = draft.any_of[groupIndex]
    if (!group?.all_of) return

    if (nextType === 'subscription') {
      group.all_of[condIndex] = defaultSubscriptionCondition()
    } else {
      group.all_of[condIndex] = defaultBalanceCondition()
    }
  })
}

function setOperator(groupIndex: number, condIndex: number, op: AnnouncementOperator) {
  updateTargeting((draft) => {
    const group = draft.any_of[groupIndex]
    if (!group?.all_of) return

    const cond = group.all_of[condIndex]
    if (!cond) return

    cond.operator = op
  })
}

function setBalanceValue(groupIndex: number, condIndex: number, raw: string) {
  const n = raw === '' ? 0 : Number(raw)
  updateTargeting((draft) => {
    const group = draft.any_of[groupIndex]
    if (!group?.all_of) return

    const cond = group.all_of[condIndex]
    if (!cond) return

    cond.value = Number.isFinite(n) ? n : 0
  })
}

// We keep group_ids selection in a parallel reactive map because GroupSelector is numeric list.
// Then we mirror it back to targeting.group_ids via a watcher.
const subscriptionSelections = reactive<Record<number, Record<number, number[]>>>({})

function ensureSelectionPath(groupIndex: number, condIndex: number) {
  if (!subscriptionSelections[groupIndex]) subscriptionSelections[groupIndex] = {}
  if (!subscriptionSelections[groupIndex][condIndex]) subscriptionSelections[groupIndex][condIndex] = []
}

// Sync from modelValue to subscriptionSelections (one-way: model -> local state)
watch(
  () => props.modelValue,
  (v) => {
    const groups = v?.any_of ?? []
    for (let gi = 0; gi < groups.length; gi++) {
      const allOf = groups[gi]?.all_of ?? []
      for (let ci = 0; ci < allOf.length; ci++) {
        const c = allOf[ci]
        if (c?.type === 'subscription') {
          ensureSelectionPath(gi, ci)
          // Only update if different to avoid triggering unnecessary updates
          const newIds = (c.group_ids ?? []).slice()
          const currentIds = subscriptionSelections[gi]?.[ci] ?? []
          if (JSON.stringify(newIds.sort()) !== JSON.stringify(currentIds.sort())) {
            subscriptionSelections[gi][ci] = newIds
          }
        }
      }
    }
  },
  { immediate: true }
)

// Sync from subscriptionSelections to modelValue (one-way: local state -> model)
// Use a debounced approach to avoid infinite loops
let syncTimeout: ReturnType<typeof setTimeout> | null = null
watch(
  () => subscriptionSelections,
  () => {
    // Debounce the sync to avoid rapid fire updates
    if (syncTimeout) clearTimeout(syncTimeout)

    syncTimeout = setTimeout(() => {
      // Build the new targeting state
      const newTargeting: TargetingDraft = JSON.parse(JSON.stringify(props.modelValue ?? { any_of: [] }))
      if (!newTargeting.any_of) newTargeting.any_of = []

      const groups = newTargeting.any_of ?? []
      for (let gi = 0; gi < groups.length; gi++) {
        const allOf = groups[gi]?.all_of ?? []
        for (let ci = 0; ci < allOf.length; ci++) {
          const c = allOf[ci]
          if (c?.type === 'subscription') {
            ensureSelectionPath(gi, ci)
            c.operator = 'in' as AnnouncementOperator
            c.group_ids = (subscriptionSelections[gi]?.[ci] ?? []).slice()
          }
        }
      }

      // Only emit if there's an actual change (deep comparison)
      if (JSON.stringify(props.modelValue) !== JSON.stringify(newTargeting)) {
        emit('update:modelValue', newTargeting)
      }
    }, 0)
  },
  { deep: true }
)

const validationError = computed(() => {
  if (mode.value !== 'custom') return ''

  const groups = anyOf.value
  if (groups.length === 0) return t('admin.announcements.form.addOrGroup')

  if (groups.length > 50) return 'any_of > 50'

  for (const g of groups) {
    const allOf = g?.all_of ?? []
    if (allOf.length === 0) return t('admin.announcements.form.addAndCondition')
    if (allOf.length > 50) return 'all_of > 50'

    for (const c of allOf) {
      if (c.type === 'subscription') {
        if (!c.group_ids || c.group_ids.length === 0) return t('admin.announcements.form.selectPackages')
      }
    }
  }

  return ''
})
</script>
