<template>
  <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
    <div class="relative max-w-md flex-1">
      <SearchInput 
        :model-value="searchQuery" 
        :placeholder="t('admin.accounts.searchAccounts')" 
        @update:model-value="$emit('update:searchQuery', $event)"
        @search="$emit('change')"
      />
    </div>
    <div class="flex gap-3">
      <Select v-model="filters.platform" :options="pOpts" @change="$emit('change')" />
      <Select v-model="filters.status" :options="sOpts" @change="$emit('change')" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'; import { useI18n } from 'vue-i18n'; import Select from '@/components/common/Select.vue'; import SearchInput from '@/components/common/SearchInput.vue'
defineProps(['searchQuery', 'filters']); defineEmits(['update:searchQuery', 'change']); const { t } = useI18n()
const pOpts = computed(() => [{ value: '', label: t('admin.accounts.allPlatforms') }, { value: 'openai', label: 'OpenAI' }, { value: 'anthropic', label: 'Anthropic' }, { value: 'gemini', label: 'Gemini' }])
const sOpts = computed(() => [{ value: '', label: t('admin.accounts.allStatus') }, { value: 'active', label: t('admin.accounts.status.active') }, { value: 'error', label: t('admin.accounts.status.error') }])
</script>
