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
  transition: color 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)), opacity 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}

.legal-footer-link.router-link-active,
.legal-footer-link:active {
  color: var(--ui-primary);
  opacity: 1;
}

@media (hover: hover) and (pointer: fine) {
  .legal-footer-link:hover {
    color: var(--ui-primary);
    opacity: 1;
  }
}

.legal-footer-link:focus-visible {
  outline: 2px solid var(--ui-primary);
  outline-offset: 2px;
  border-radius: 4px;
}

@media (pointer: coarse) {
  .legal-footer-link {
    display: inline-flex;
    align-items: center;
    min-height: 2.75rem;
  }
}
</style>
