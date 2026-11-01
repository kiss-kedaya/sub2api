<template>
  <!-- Stacked fields for the admin user dialog; values update the map, then emit it. -->
  <div v-if="attributes.length > 0" class="space-y-4">
    <UFormField
      v-for="attr in attributes"
      :key="attr.id"
      :label="attr.name"
      :required="attr.required"
      :help="attr.description || undefined"
    >
      <!-- Text Input -->
      <UInput
        v-if="attr.type === 'text' || attr.type === 'email' || attr.type === 'url'"
        :model-value="localValues[attr.id]"
        :type="attr.type === 'text' ? 'text' : attr.type"
        :required="attr.required"
        :placeholder="attr.placeholder"
        class="w-full"
        @update:model-value="setValue(attr.id, $event)"
      />

      <!-- Number Input -->
      <UInput
        v-else-if="attr.type === 'number'"
        :model-value="localValues[attr.id]"
        type="number"
        :required="attr.required"
        :placeholder="attr.placeholder"
        :min="attr.validation?.min"
        :max="attr.validation?.max"
        class="w-full"
        @update:model-value="setValue(attr.id, $event)"
      />

      <!-- Date Input -->
      <UInput
        v-else-if="attr.type === 'date'"
        :model-value="localValues[attr.id]"
        type="date"
        :required="attr.required"
        class="w-full"
        @update:model-value="setValue(attr.id, $event)"
      />

      <!-- Textarea -->
      <UTextarea
        v-else-if="attr.type === 'textarea'"
        :model-value="localValues[attr.id]"
        :required="attr.required"
        :placeholder="attr.placeholder"
        :rows="3"
        class="w-full"
        @update:model-value="setValue(attr.id, $event)"
      />

      <!-- Select -->
      <Select
        v-else-if="attr.type === 'select'"
        v-model="localValues[attr.id]"
        :options="attr.options || []"
        @change="emitChange"
      />

      <!-- Multi-Select (Checkboxes) -->
      <div v-else-if="attr.type === 'multi_select'" class="space-y-2">
        <UCheckbox
          v-for="opt in attr.options"
          :key="opt.value"
          :label="opt.label"
          :model-value="isOptionSelected(attr.id, opt.value)"
          @update:model-value="toggleMultiSelectOption(attr.id, opt.value)"
        />
      </div>
    </UFormField>
  </div>

  <!-- Loading State -->
  <div v-else-if="loading" class="flex justify-center py-4">
    <UIcon name="i-lucide-loader-circle" class="size-5 animate-spin text-dimmed" />
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { adminAPI } from '@/api/admin'
import type { UserAttributeDefinition, UserAttributeValuesMap } from '@/types'
import Select from '@/components/common/Select.vue'

interface Props {
  userId?: number
  modelValue: UserAttributeValuesMap
}

interface Emits {
  (e: 'update:modelValue', value: UserAttributeValuesMap): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const loading = ref(false)
const attributes = ref<UserAttributeDefinition[]>([])
const localValues = ref<UserAttributeValuesMap>({})

const loadAttributes = async () => {
  loading.value = true
  try {
    attributes.value = await adminAPI.userAttributes.listEnabledDefinitions()
  } catch (error) {
    console.error('Failed to load attributes:', error)
  } finally {
    loading.value = false
  }
}

const loadUserValues = async () => {
  if (!props.userId) return

  try {
    const values = await adminAPI.userAttributes.getUserAttributeValues(props.userId)
    const valuesMap: UserAttributeValuesMap = {}
    values.forEach(v => {
      valuesMap[v.attribute_id] = v.value
    })
    localValues.value = { ...valuesMap }
    emit('update:modelValue', localValues.value)
  } catch (error) {
    console.error('Failed to load user attribute values:', error)
  }
}

const emitChange = () => {
  emit('update:modelValue', { ...localValues.value })
}

// Number fields arrive as numbers (as with the former v-model.number); stored as before.
const setValue = (attrId: number, value: unknown) => {
  localValues.value[attrId] = value as string
  emitChange()
}

const isOptionSelected = (attrId: number, optionValue: string): boolean => {
  const value = localValues.value[attrId]
  if (!value) return false
  try {
    const arr = JSON.parse(value)
    return Array.isArray(arr) && arr.includes(optionValue)
  } catch {
    return false
  }
}

const toggleMultiSelectOption = (attrId: number, optionValue: string) => {
  let arr: string[] = []
  const value = localValues.value[attrId]
  if (value) {
    try {
      arr = JSON.parse(value)
      if (!Array.isArray(arr)) arr = []
    } catch {
      arr = []
    }
  }

  const index = arr.indexOf(optionValue)
  if (index > -1) {
    arr.splice(index, 1)
  } else {
    arr.push(optionValue)
  }

  localValues.value[attrId] = JSON.stringify(arr)
  emitChange()
}

watch(() => props.modelValue, (newVal) => {
  if (newVal && Object.keys(newVal).length > 0) {
    localValues.value = { ...newVal }
  }
}, { immediate: true })

watch(() => props.userId, (newUserId) => {
  if (newUserId) {
    loadUserValues()
  } else {
    // Reset for new user
    localValues.value = {}
  }
}, { immediate: true })

onMounted(() => {
  loadAttributes()
})
</script>
