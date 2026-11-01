<template>
  <div>
    <!-- Tags + inline input, drawn like a Nuxt UI outline input -->
    <div class="flex min-h-9 flex-wrap items-center gap-1.5 rounded-md bg-default px-2 py-1.5 ring ring-inset ring-accented focus-within:ring-2 focus-within:ring-primary">
      <UBadge
        v-for="(model, idx) in models"
        :key="idx"
        color="neutral"
        variant="subtle"
        class="gap-0.5 pe-0.5"
      >
        {{ model }}
        <UButton
          icon="i-lucide-x"
          color="neutral"
          variant="link"
          size="xs"
          class="p-0.5"
          :ui="{ leadingIcon: 'size-3' }"
          :aria-label="t('common.remove')"
          @click="removeModel(idx)"
        />
      </UBadge>
      <UInput
        ref="inputRef"
        v-model="inputValue"
        type="text"
        variant="none"
        class="min-w-[120px] flex-1"
        :ui="{ base: 'px-1 py-0.5' }"
        :placeholder="models.length === 0 ? placeholder : ''"
        @keydown.enter.prevent="addModel"
        @keydown.tab="handleTab"
        @keydown.delete="handleBackspace"
        @paste="handlePaste"
        @blur="addModel"
      />
    </div>
    <p class="mt-1 text-xs text-muted">
      {{ t('admin.channels.form.modelInputHint', 'Press Enter to add, supports paste for batch import.') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = defineProps<{
  models: string[]
  placeholder?: string
  platform?: string
}>()

const emit = defineEmits<{
  'update:models': [models: string[]]
}>()

const inputValue = ref('')
const inputRef = ref<{ inputRef?: HTMLInputElement | null } | null>(null)

function addModel() {
  const val = inputValue.value.trim()
  if (!val) return
  if (!props.models.includes(val)) {
    emit('update:models', [...props.models, val])
  }
  inputValue.value = ''
}

function handleTab(event: KeyboardEvent) {
  if (!inputValue.value.trim()) return
  event.preventDefault()
  addModel()
}

function removeModel(idx: number) {
  const newModels = [...props.models]
  newModels.splice(idx, 1)
  emit('update:models', newModels)
}

function handleBackspace() {
  if (inputValue.value === '' && props.models.length > 0) {
    removeModel(props.models.length - 1)
  }
}

function handlePaste(e: ClipboardEvent) {
  e.preventDefault()
  const text = e.clipboardData?.getData('text') || ''
  const items = text.split(/[,\n;]+/).map(s => s.trim()).filter(Boolean)
  if (items.length === 0) return
  const unique = [...new Set([...props.models, ...items])]
  emit('update:models', unique)
  inputValue.value = ''
}
</script>
