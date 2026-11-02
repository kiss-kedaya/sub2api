<template>
  <UDropdownMenu v-model:open="isOpen" :items="items" :content="{ align: 'end' }" :ui="{ content: 'w-36' }">
    <UButton
      color="neutral"
      variant="ghost"
      :disabled="switching"
      :title="currentLocale?.name"
      class="gap-1.5 px-2 font-medium text-muted hover:text-highlighted"
    >
      <span class="text-base leading-none">{{ currentLocale?.flag }}</span>
      <span class="hidden sm:inline">{{ currentLocale?.code.toUpperCase() }}</span>
      <UIcon
        name="i-lucide-chevron-down"
        class="size-3.5 text-dimmed transition-transform duration-200"
        :class="{ 'rotate-180': isOpen }"
      />
    </UButton>

    <template #item-leading="{ item }">
      <span class="text-base leading-none">{{ (item as LocaleItem).flag }}</span>
    </template>
  </UDropdownMenu>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem } from '@nuxt/ui'
import { setLocale, availableLocales } from '@/i18n'

type LocaleItem = DropdownMenuItem & { flag: string }

const { locale } = useI18n()

const isOpen = ref(false)
const switching = ref(false)

const currentLocaleCode = computed(() => locale.value)
const currentLocale = computed(() => availableLocales.find((l) => l.code === locale.value))

const items = computed<LocaleItem[]>(() =>
  availableLocales.map((option) => ({
    label: option.name,
    flag: option.flag,
    type: 'checkbox' as const,
    checked: option.code === currentLocaleCode.value,
    disabled: switching.value,
    onSelect: () => selectLocale(option.code)
  }))
)

async function selectLocale(code: string) {
  if (switching.value || code === currentLocaleCode.value) {
    isOpen.value = false
    return
  }
  switching.value = true
  try {
    await setLocale(code)
    isOpen.value = false
  } finally {
    switching.value = false
  }
}
</script>
