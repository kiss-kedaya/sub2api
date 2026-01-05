<template>
  <div class="flex flex-wrap items-start gap-3">
    <div class="min-w-0 flex-1">
      <SearchInput
        :model-value="searchQuery"
        :placeholder="t('admin.accounts.searchAccounts')"
        @update:model-value="$emit('update:searchQuery', $event)"
        @search="$emit('change')"
      />
    </div>
    <div class="flex flex-wrap items-center gap-3">
      <Select v-model="filters.platform" class="w-40 flex-shrink-0" :options="pOpts" @change="$emit('change')" />
      <Select v-model="filters.status" class="w-40 flex-shrink-0" :options="sOpts" @change="$emit('change')" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'; import { useI18n } from 'vue-i18n'; import Select from '@/components/common/Select.vue'; import SearchInput from '@/components/common/SearchInput.vue'
defineProps(['searchQuery', 'filters']); defineEmits(['update:searchQuery', 'change']); const { t } = useI18n()
const pOpts = computed(() => [{ value: '', label: t('admin.accounts.allPlatforms') }, { value: 'openai', label: 'OpenAI' }, { value: 'anthropic', label: 'Anthropic' }, { value: 'gemini', label: 'Gemini' }])
const sOpts = computed(() => [{ value: '', label: t('admin.accounts.allStatus') }, { value: 'active', label: t('admin.accounts.status.active') }, { value: 'error', label: t('admin.accounts.status.error') }])
</script>
