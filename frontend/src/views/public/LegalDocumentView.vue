<template>
  <div class="legal-shell">
    <div class="legal-grid" aria-hidden="true"></div>
    <header class="legal-topbar">
      <div class="legal-topbar-inner">
        <RouterLink to="/home" class="legal-brand">
          <span class="legal-logo rounded-xl">
            <img :src="siteLogo || '/logo.svg'" alt="" />
          </span>
          <span class="legal-brand-name">{{ siteName }}</span>
        </RouterLink>
        <div class="legal-topbar-actions">
          <LocaleSwitcher />
          <RouterLink to="/login" class="legal-login rounded-xl">{{ t('home.login') }}</RouterLink>
        </div>
      </div>
    </header>

    <div class="legal-layout">
      <nav class="legal-sidebar" :aria-label="t('legal.navLabel')">
        <p class="legal-sidebar-kicker">{{ t('legal.navLabel') }}</p>
        <ul>
          <li v-for="doc in sidebarDocuments" :key="doc.id">
            <RouterLink
              :to="`/legal/${doc.id}`"
              class="legal-nav-link"
              :class="{ 'is-active': doc.id === activeId }"
            >
              {{ doc.title }}
            </RouterLink>
          </li>
        </ul>
      </nav>

      <main class="legal-main">
        <div v-if="loading" class="legal-panel legal-panel--center rounded-xl">
          <div class="legal-spinner"></div>
        </div>

        <section v-else-if="!currentDocument" class="legal-panel rounded-xl">
          <h1>{{ t('legal.notFound') }}</h1>
          <p>{{ t('legal.notFoundDescription') }}</p>
        </section>

        <article v-else class="legal-panel legal-panel--article rounded-xl">
          <p v-if="updatedAt" class="legal-meta">{{ t('legal.updatedAt', { date: updatedAt }) }}</p>
          <h1 class="legal-title">{{ currentDocument.title }}</h1>
          <div
            v-if="hasContent"
            class="legal-document-content"
            v-html="renderedHtml"
          ></div>
          <p v-else class="legal-empty">{{ t('legal.empty') }}</p>
          <p class="legal-end">
            &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
          </p>
        </article>
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useI18n } from 'vue-i18n'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
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
.legal-shell {
  position: relative;
  isolation: isolate;
  min-height: 100dvh;
  background: var(--app-page-bg);
  color: var(--ui-text-highlighted);
}

.legal-grid {
  pointer-events: none;
  position: absolute;
  inset: 0;
  z-index: 0;
  background-image:
    linear-gradient(color-mix(in oklab, var(--ui-border) 55%, transparent) 1px, transparent 1px),
    linear-gradient(90deg, color-mix(in oklab, var(--ui-border) 55%, transparent) 1px, transparent 1px);
  background-size: 48px 48px;
  mask-image: linear-gradient(to bottom, black 0%, rgba(0, 0, 0, 0.45) 72%, transparent 100%);
}

.legal-layout {
  position: relative;
  z-index: 1;
}

html.dark .legal-grid {
  background-image:
    linear-gradient(color-mix(in oklab, var(--ui-border-accented) 42%, transparent) 1px, transparent 1px),
    linear-gradient(90deg, color-mix(in oklab, var(--ui-border-accented) 42%, transparent) 1px, transparent 1px);
}

.legal-topbar {
  position: sticky;
  top: 0;
  z-index: 40;
  border-bottom: 1px solid var(--ui-border);
  background: color-mix(in srgb, var(--app-page-bg) 88%, transparent);
  backdrop-filter: blur(16px);
}

@media (prefers-reduced-transparency: reduce) {
  .legal-topbar {
    background: var(--app-page-bg);
    backdrop-filter: none;
  }
}

.legal-topbar-inner,
.legal-layout {
  margin: 0 auto;
  max-width: 1200px;
}

.legal-topbar-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.9rem 1.25rem;
}

.legal-brand {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.75rem;
  border-radius: 0.5rem;
  color: inherit;
  text-decoration: none;
}

.legal-brand:focus-visible,
.legal-login:focus-visible,
.legal-nav-link:focus-visible,
.legal-document-content :deep(a):focus-visible {
  outline: 2px solid var(--ui-primary);
  outline-offset: 2px;
}

.legal-logo {
  display: flex;
  height: 2.25rem;
  width: 2.25rem;
  flex-shrink: 0;
  overflow: hidden;
  border-radius: 0.75rem;
  background: var(--ui-bg);
  box-shadow: 0 0 0 1px var(--ui-border);
}

.legal-logo img {
  height: 100%;
  width: 100%;
  object-fit: contain;
}

.legal-brand-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.95rem;
  font-weight: 600;
}

.legal-topbar-actions {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.legal-login {
  display: inline-flex;
  min-height: 2.25rem;
  align-items: center;
  border-radius: 0.75rem;
  background: var(--ui-primary);
  padding: 0 0.95rem;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--ui-text-inverted);
  text-decoration: none;
  transition:
    background-color 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)),
    transform 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}

.legal-login:active {
  transform: scale(0.97);
}

.legal-layout {
  display: flex;
  gap: 2.5rem;
  padding: 2.5rem 1.25rem 4rem;
}

.legal-sidebar {
  position: sticky;
  top: 5.5rem;
  height: calc(100dvh - 7rem);
  width: 16.5rem;
  flex-shrink: 0;
  overflow-y: auto;
}

.legal-sidebar-kicker {
  margin-bottom: 0.85rem;
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--ui-text-muted);
}

.legal-sidebar ul {
  list-style: none;
  margin: 0;
  padding: 0;
}

.legal-nav-link {
  position: relative;
  display: block;
  border-radius: 0.375rem;
  padding: 0.45rem 0 0.45rem 0.85rem;
  color: var(--ui-text-muted);
  font-size: 0.92rem;
  text-decoration: none;
  transition: color 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}

.legal-nav-link.is-active {
  color: var(--ui-primary);
}

@media (hover: hover) and (pointer: fine) {
  .legal-login:hover {
    background: color-mix(in srgb, var(--ui-primary) 88%, var(--ui-text-highlighted));
  }

  .legal-nav-link:hover {
    color: var(--ui-primary);
  }
}

.legal-nav-link.is-active::before {
  position: absolute;
  top: 0.5rem;
  bottom: 0.5rem;
  left: 0;
  width: 2px;
  border-radius: 9999px;
  corner-shape: inherit;
  background: var(--ui-primary);
  content: '';
}

.legal-main {
  min-width: 0;
  flex: 1;
}

.legal-panel {
  max-width: 50rem;
  animation: legal-in 220ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
  border: 1px solid var(--ui-border);
  border-radius: 0.75rem;
  background: var(--ui-bg);
  box-shadow: var(--app-shadow-card);
  padding: 2.5rem;
}

.legal-panel--center {
  display: flex;
  min-height: 20rem;
  align-items: center;
  justify-content: center;
}

.legal-panel--error {
  border-color: color-mix(in srgb, var(--ui-error) 35%, transparent);
  background: color-mix(in srgb, var(--ui-error) 8%, transparent);
}

.legal-spinner {
  height: 2rem;
  width: 2rem;
  border-radius: 999px;
  border: 2px solid var(--ui-primary);
  border-top-color: transparent;
  animation: legal-spin 0.8s linear infinite;
}

.legal-meta {
  margin-bottom: 0.75rem;
  color: var(--ui-text-muted);
  font-size: 0.875rem;
}

.legal-title {
  margin: 0 0 1.25rem;
  font-size: clamp(1.75rem, 3vw, 2.15rem);
  font-weight: 700;
  letter-spacing: -0.03em;
  line-height: 1.2;
}

.legal-empty,
.legal-end {
  color: var(--ui-text-muted);
  font-size: 0.85rem;
}

.legal-end {
  margin-top: 2.5rem;
  border-top: 1px solid var(--ui-border);
  padding-top: 1.25rem;
  text-align: center;
}

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
  font-size: 1.35rem;
  font-weight: 600;
}

.legal-document-content :deep(h3) {
  margin: 1.4rem 0 0.55rem;
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

.legal-document-content :deep(li) {
  margin-bottom: 0.4rem;
}

.legal-document-content :deep(strong) {
  color: inherit;
  font-weight: 650;
}

.legal-document-content :deep(a) {
  color: var(--ui-primary);
  text-decoration: underline;
  text-underline-offset: 3px;
}

.legal-document-content :deep(blockquote) {
  margin: 0 0 1.25rem;
  border: 1px solid color-mix(in srgb, var(--ui-primary) 22%, transparent);
  border-radius: 0.5rem;
  background: color-mix(in srgb, var(--ui-primary) 8%, transparent);
  padding: 0.95rem 1.1rem;
  color: var(--ui-text-toned);
}

.legal-document-content :deep(blockquote p:last-child) {
  margin-bottom: 0;
}

@keyframes legal-in {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

@keyframes legal-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (pointer: coarse), (max-width: 768px) {
  .legal-login {
    min-height: 2.75rem;
  }
}

@media (max-width: 768px) {
  .legal-layout {
    flex-direction: column;
    gap: 1.25rem;
    padding: 1.25rem 0.9rem 3rem;
  }

  .legal-sidebar {
    position: static;
    height: auto;
    width: auto;
    overflow-x: auto;
  }

  .legal-sidebar-kicker {
    display: none;
  }

  .legal-sidebar ul {
    display: flex;
    gap: 0.4rem;
  }

  .legal-nav-link {
    display: inline-flex;
    min-height: 2.75rem;
    align-items: center;
    white-space: nowrap;
    border-radius: 999px;
    background: color-mix(in srgb, var(--ui-bg) 90%, transparent);
    box-shadow: 0 0 0 1px var(--ui-border);
    padding: 0.4rem 0.9rem;
  }

  .legal-nav-link.is-active::before {
    display: none;
  }

  .legal-nav-link.is-active {
    background: var(--ui-primary);
    color: var(--ui-text-inverted);
    box-shadow: 0 0 0 1px var(--ui-primary);
  }

  .legal-panel {
    padding: 1.5rem;
  }
}

@media (prefers-reduced-motion: reduce) {
  .legal-panel {
    animation: none;
  }

  .legal-spinner {
    animation: none;
    opacity: 0.75;
  }

  .legal-login,
  .legal-nav-link {
    transition-duration: 120ms;
  }

  .legal-login:active {
    transform: none;
  }
}
</style>
