<template>
  <div class="min-h-screen bg-default text-default">
    <PublicHeader :site-name="siteName" :logo="siteLogo" brand-to="/home">
      <template #actions>
        <LocaleSwitcher />
        <UButton to="/login" :label="t('home.login')" color="neutral" class="ms-1" />
      </template>
    </PublicHeader>

    <UContainer>
      <UPage>
        <template #left>
          <aside class="py-6 lg:sticky lg:top-16 lg:max-h-[calc(100vh-4rem)] lg:overflow-y-auto lg:py-8" :aria-label="t('legal.navLabel')">
            <p class="mb-2 px-2.5 text-xs font-semibold text-highlighted">{{ t('legal.navLabel') }}</p>
            <UNavigationMenu :items="sidebarItems" orientation="vertical" />
          </aside>
        </template>

        <main class="min-w-0 pb-16">
          <div v-if="loading" class="flex min-h-80 items-center justify-center">
            <UIcon name="i-lucide-loader-circle" class="size-8 animate-spin text-muted" />
          </div>

          <section v-else-if="!currentDocument" class="py-8">
            <h1 class="text-2xl font-bold text-highlighted">{{ t('legal.notFound') }}</h1>
            <p class="mt-3 text-muted">{{ t('legal.notFoundDescription') }}</p>
          </section>

          <article v-else class="max-w-3xl">
            <UPageHeader
              :headline="updatedAt ? t('legal.updatedAt', { date: updatedAt }) : undefined"
              :title="currentDocument.title"
              :ui="{ headline: 'font-normal text-muted' }"
            />
            <div
              v-if="hasContent"
              class="legal-document-content mt-8"
              v-html="renderedHtml"
            ></div>
            <p v-else class="mt-8 text-sm text-muted">{{ t('legal.empty') }}</p>
            <p class="mt-12 border-t border-default pt-5 text-center text-sm text-muted">
              &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
            </p>
          </article>
        </main>
      </UPage>
    </UContainer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useI18n } from 'vue-i18n'
import type { NavigationMenuItem } from '@nuxt/ui'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import PublicHeader from '@/views/public/components/PublicHeader.vue'
import { getLocale } from '@/i18n'
import { LEGAL_UPDATED_AT, mergeLegalDocuments, resolveLegalDocumentId } from '@/legal/catalog'
import { sanitizeUrl } from '@/utils/url'
import { useAppStore } from '@/stores/app'
import type { LoginAgreementDocument } from '@/types'
import zhAdminCompliance from '../../../../docs/legal/admin-compliance.zh.md?raw'
import enAdminCompliance from '../../../../docs/legal/admin-compliance.en.md?raw'

const route = useRoute()
const { t } = useI18n()
const appStore = useAppStore()
const settings = computed(() => appStore.cachedPublicSettings)
const loading = ref(!settings.value)
const currentYear = computed(() => new Date().getFullYear())

marked.setOptions({
  breaks: true,
  gfm: true,
})

const documentId = computed(() => String(route.params.documentId || ''))
const isAdminComplianceDocument = computed(() => documentId.value === 'admin-compliance')
const locale = computed(() => getLocale())
const siteName = computed(() => settings.value?.site_name || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(settings.value?.site_logo || '', {
  allowRelative: true,
  allowDataUrl: true,
}))

const sidebarDocuments = computed(() => {
  if (isAdminComplianceDocument.value) {
    return [{
      id: 'admin-compliance',
      title: t('legal.adminCompliance'),
      content_md: locale.value === 'zh' ? zhAdminCompliance : enAdminCompliance,
    }]
  }
  return mergeLegalDocuments(locale.value, settings.value?.login_agreement_documents)
})

const activeId = computed(() => {
  if (isAdminComplianceDocument.value) return 'admin-compliance'
  return resolveLegalDocumentId(documentId.value) || documentId.value
})

const sidebarItems = computed<NavigationMenuItem[]>(() => sidebarDocuments.value.map((doc) => ({
  label: doc.title,
  to: `/legal/${doc.id}`,
  active: doc.id === activeId.value,
})))

const currentDocument = computed<LoginAgreementDocument | null>(() => {
  if (isAdminComplianceDocument.value) {
    return {
      id: 'admin-compliance',
      title: t('adminCompliance.title'),
      content_md: locale.value === 'zh' ? zhAdminCompliance : enAdminCompliance,
    }
  }
  const canonical = resolveLegalDocumentId(documentId.value) || documentId.value
  return sidebarDocuments.value.find((doc) => doc.id === canonical) ?? null
})

const updatedAt = computed(() => {
  if (isAdminComplianceDocument.value) return ''
  return settings.value?.login_agreement_updated_at || LEGAL_UPDATED_AT
})

const hasContent = computed(() => Boolean(currentDocument.value?.content_md?.trim()))

const renderedHtml = computed(() => {
  const content = currentDocument.value?.content_md?.trim() || ''
  if (!content) return ''
  const html = marked.parse(content) as string
  return DOMPurify.sanitize(html)
})

onMounted(async () => {
  try {
    await appStore.fetchPublicSettings()
  } catch {
    // Keep bundled terms visible when public settings are unavailable.
  } finally {
    loading.value = false
  }
})
</script>

<style scoped>
/* Markdown prose in Nuxt UI tokens, so light and dark follow the site theme. */
.legal-document-content {
  line-height: 1.75;
  overflow-wrap: anywhere;
}

.legal-document-content :deep(h1) {
  display: none;
}

.legal-document-content :deep(h2) {
  margin: 2.2rem 0 0.8rem;
  border-bottom: 1px solid var(--ui-border);
  padding-bottom: 0.45rem;
  color: var(--ui-text-highlighted);
  font-size: 1.35rem;
  font-weight: 600;
}

.legal-document-content :deep(h3) {
  margin: 1.4rem 0 0.55rem;
  color: var(--ui-text-highlighted);
  font-size: 1.05rem;
  font-weight: 600;
}

.legal-document-content :deep(p),
.legal-document-content :deep(li) {
  color: var(--ui-text-toned);
}

.legal-document-content :deep(p) {
  margin-bottom: 1rem;
}

.legal-document-content :deep(ul),
.legal-document-content :deep(ol) {
  margin: 0 0 1rem 1.2rem;
}

.legal-document-content :deep(ul) {
  list-style: disc;
}

.legal-document-content :deep(ol) {
  list-style: decimal;
}

.legal-document-content :deep(li) {
  margin-bottom: 0.4rem;
}

.legal-document-content :deep(strong) {
  color: var(--ui-text-highlighted);
  font-weight: 650;
}

.legal-document-content :deep(a) {
  color: var(--ui-primary);
  text-decoration: underline;
  text-underline-offset: 3px;
}

.legal-document-content :deep(blockquote) {
  margin: 0 0 1.25rem;
  border-inline-start: 3px solid var(--ui-border-accented);
  border-radius: 0.375rem;
  background: var(--ui-bg-muted);
  padding: 0.85rem 1rem;
  color: var(--ui-text-toned);
}

.legal-document-content :deep(blockquote p:last-child) {
  margin-bottom: 0;
}
</style>
