<template>
  <!-- UFileUpload owns the hidden file input; the value stays a data URL / SVG string. -->
  <UFileUpload v-model="picked" :accept="acceptTypes" :dropzone="false" reset class="w-full">
    <template #default="{ open }">
      <div class="flex items-start gap-4">
        <!-- Preview Box -->
        <div class="shrink-0">
          <div
            class="flex items-center justify-center overflow-hidden rounded-lg bg-elevated ring ring-default"
            :class="previewSizeClass"
          >
            <!-- SVG mode: render inline -->
            <span
              v-if="mode === 'svg' && modelValue"
              class="text-muted [&>svg]:h-full [&>svg]:w-full"
              :class="innerSizeClass"
              v-html="sanitizedValue"
            ></span>
            <!-- Image mode: show as img -->
            <img
              v-else-if="mode === 'image' && modelValue"
              :src="modelValue"
              alt=""
              class="h-full w-full object-contain"
            />
            <!-- Empty placeholder -->
            <UIcon v-else name="i-lucide-image" class="text-dimmed" :class="placeholderSizeClass" />
          </div>
        </div>

        <!-- Controls -->
        <div class="min-w-0 flex-1 space-y-2">
          <div class="flex flex-wrap items-center gap-2">
            <UButton
              color="neutral"
              variant="outline"
              size="sm"
              icon="i-lucide-upload"
              :label="resolvedUploadLabel"
              @click="open()"
            />
            <UButton
              v-if="modelValue"
              color="error"
              variant="soft"
              size="sm"
              icon="i-lucide-trash-2"
              :label="resolvedRemoveLabel"
              @click="$emit('update:modelValue', '')"
            />
          </div>
          <p v-if="hint" class="text-xs text-muted">{{ hint }}</p>
          <p v-if="error" class="text-xs text-error">{{ error }}</p>
        </div>
      </div>
    </template>
  </UFileUpload>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { sanitizeSvg } from '@/utils/sanitize'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  modelValue: string
  mode?: 'image' | 'svg'
  size?: 'sm' | 'md'
  uploadLabel?: string
  removeLabel?: string
  hint?: string
  maxSize?: number // bytes
}>(), {
  mode: 'image',
  size: 'md',
  uploadLabel: '',
  removeLabel: '',
  hint: '',
  maxSize: 300 * 1024,
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const error = ref('')
const picked = ref<File | null>(null)

const resolvedUploadLabel = computed(() => props.uploadLabel || t('common.upload'))
const resolvedRemoveLabel = computed(() => props.removeLabel || t('common.remove'))

const acceptTypes = computed(() => props.mode === 'svg' ? '.svg' : 'image/*')

const sanitizedValue = computed(() =>
  props.mode === 'svg' ? sanitizeSvg(props.modelValue ?? '') : ''
)

const previewSizeClass = computed(() => props.size === 'sm' ? 'h-14 w-14' : 'h-20 w-20')
const innerSizeClass = computed(() => props.size === 'sm' ? 'h-7 w-7' : 'h-12 w-12')
const placeholderSizeClass = computed(() => props.size === 'sm' ? 'h-5 w-5' : 'h-8 w-8')

// Each pick is read once, then cleared so the same file can be chosen again.
watch(picked, (file) => {
  if (!file) return
  picked.value = null
  handleFile(file)
})

function handleFile(file: File) {
  error.value = ''

  if (props.maxSize && file.size > props.maxSize) {
    error.value = t('common.fileTooLargeKb', {
      size: (file.size / 1024).toFixed(1),
      max: (props.maxSize / 1024).toFixed(0)
    })
    return
  }

  const reader = new FileReader()
  if (props.mode === 'svg') {
    reader.onload = (e) => {
      const text = e.target?.result as string
      if (text) emit('update:modelValue', text.trim())
    }
    reader.readAsText(file)
  } else {
    if (!file.type.startsWith('image/')) {
      error.value = t('common.selectImageFile')
      return
    }
    reader.onload = (e) => {
      emit('update:modelValue', e.target?.result as string)
    }
    reader.readAsDataURL(file)
  }

  reader.onerror = () => {
    error.value = t('common.fileReadFailed')
  }
}
</script>
