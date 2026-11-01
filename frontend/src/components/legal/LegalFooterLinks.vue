<template>
  <nav class="flex flex-wrap items-center justify-center gap-x-4 gap-y-1.5" :aria-label="t('legal.navLabel')">
    <ULink
      v-for="doc in documents"
      :key="doc.id"
      :to="`/legal/${doc.id}`"
      class="text-[13px] leading-snug"
    >
      {{ doc.title }}
    </ULink>
  </nav>
</template>

<script setup lang="ts">
// Legal policy links for page footers (home, sign-in layout). No props.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { getLocale } from '@/i18n'
import { bundledLegalDocuments } from '@/legal/catalog'

const { t } = useI18n()
const documents = computed(() => bundledLegalDocuments(getLocale()))
</script>
