<template>
  <nav class="legal-footer-links" :aria-label="t('legal.navLabel')">
    <RouterLink
      v-for="doc in documents"
      :key="doc.id"
      :to="`/legal/${doc.id}`"
      class="legal-footer-link"
    >
      {{ doc.title }}
    </RouterLink>
  </nav>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { getLocale } from '@/i18n'
import { bundledLegalDocuments } from '@/legal/catalog'

const { t } = useI18n()
const documents = computed(() => bundledLegalDocuments(getLocale()))
</script>

<style scoped>
.legal-footer-links {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 0.35rem 1rem;
}

.legal-footer-link {
  color: inherit;
  font-size: 0.8125rem;
  line-height: 1.4;
  opacity: 0.72;
  text-decoration: none;
  transition: color 0.2s ease, opacity 0.2s ease;
}

.legal-footer-link:hover,
.legal-footer-link.router-link-active {
  color: rgb(59 130 246);
  opacity: 1;
}
</style>
