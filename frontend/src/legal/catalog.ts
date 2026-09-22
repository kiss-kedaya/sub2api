import privacyZh from '../../../docs/legal/privacy.zh.md?raw'
import privacyEn from '../../../docs/legal/privacy.en.md?raw'
import termsZh from '../../../docs/legal/terms.zh.md?raw'
import termsEn from '../../../docs/legal/terms.en.md?raw'
import aupZh from '../../../docs/legal/aup.zh.md?raw'
import aupEn from '../../../docs/legal/aup.en.md?raw'
import disclaimerZh from '../../../docs/legal/disclaimer.zh.md?raw'
import disclaimerEn from '../../../docs/legal/disclaimer.en.md?raw'
import refundZh from '../../../docs/legal/refund.zh.md?raw'
import refundEn from '../../../docs/legal/refund.en.md?raw'
import dpaZh from '../../../docs/legal/dpa.zh.md?raw'
import dpaEn from '../../../docs/legal/dpa.en.md?raw'
import complianceZh from '../../../docs/legal/compliance.zh.md?raw'
import complianceEn from '../../../docs/legal/compliance.en.md?raw'
import supportedRegionsZh from '../../../docs/legal/supported-regions.zh.md?raw'
import supportedRegionsEn from '../../../docs/legal/supported-regions.en.md?raw'
import type { LoginAgreementDocument } from '@/types'

export const LEGAL_UPDATED_AT = '2026-09-22'

export const LEGAL_DOCUMENT_IDS = [
  'privacy',
  'terms',
  'aup',
  'disclaimer',
  'refund',
  'dpa',
  'compliance',
  'supported-regions',
] as const

export type LegalDocumentId = (typeof LEGAL_DOCUMENT_IDS)[number]

const ALIASES: Record<string, LegalDocumentId> = {
  'privacy-policy': 'privacy',
  'terms-of-service': 'terms',
  'usage-policy': 'aup',
  'acceptable-use': 'aup',
  'acceptable-use-policy': 'aup',
  'service-specific-terms': 'disclaimer',
  'refund-policy': 'refund',
  'supported-countries': 'supported-regions',
  'supported-countries-and-regions': 'supported-regions',
}

type BundledBody = Record<LegalDocumentId, { zh: string; en: string }>

const BODIES: BundledBody = {
  privacy: { zh: privacyZh, en: privacyEn },
  terms: { zh: termsZh, en: termsEn },
  aup: { zh: aupZh, en: aupEn },
  disclaimer: { zh: disclaimerZh, en: disclaimerEn },
  refund: { zh: refundZh, en: refundEn },
  dpa: { zh: dpaZh, en: dpaEn },
  compliance: { zh: complianceZh, en: complianceEn },
  'supported-regions': { zh: supportedRegionsZh, en: supportedRegionsEn },
}

export const LEGAL_TITLES: Record<LegalDocumentId, { zh: string; en: string }> = {
  privacy: { zh: '隐私政策', en: 'Privacy Policy' },
  terms: { zh: '服务条款', en: 'Terms of Service' },
  aup: { zh: '可接受使用政策', en: 'Acceptable Use Policy' },
  disclaimer: { zh: '免责条款', en: 'Disclaimer' },
  refund: { zh: '退款政策', en: 'Refund Policy' },
  dpa: { zh: '数据处理协议', en: 'Data Processing Agreement' },
  compliance: { zh: '合规与业务说明', en: 'Compliance Statement' },
  'supported-regions': { zh: '支持的国家和地区', en: 'Supported Countries and Regions' },
}

export function normalizeLegalDocumentId(raw: string): string {
  return raw
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/[-_]{2,}/g, '-')
    .replace(/^[-_]+|[-_]+$/g, '')
}

export function resolveLegalDocumentId(raw: string): LegalDocumentId | null {
  const id = normalizeLegalDocumentId(raw)
  if ((LEGAL_DOCUMENT_IDS as readonly string[]).includes(id)) {
    return id as LegalDocumentId
  }
  return ALIASES[id] ?? null
}

export function bundledLegalTitle(id: LegalDocumentId, locale: string): string {
  return locale.toLowerCase().startsWith('zh') ? LEGAL_TITLES[id].zh : LEGAL_TITLES[id].en
}

export function bundledLegalMarkdown(id: LegalDocumentId, locale: string): string {
  return locale.toLowerCase().startsWith('zh') ? BODIES[id].zh : BODIES[id].en
}

export function bundledLegalDocuments(locale: string): LoginAgreementDocument[] {
  return LEGAL_DOCUMENT_IDS.map((id) => ({
    id,
    title: bundledLegalTitle(id, locale),
    content_md: bundledLegalMarkdown(id, locale),
  }))
}

export function mergeLegalDocuments(
  locale: string,
  adminDocuments: LoginAgreementDocument[] | undefined,
): LoginAgreementDocument[] {
  const bundled = bundledLegalDocuments(locale)
  const extras: LoginAgreementDocument[] = []
  for (const doc of adminDocuments ?? []) {
    const canonical = resolveLegalDocumentId(doc.id || '')
    if (canonical) {
      if (doc.content_md?.trim()) {
        const index = bundled.findIndex((item) => item.id === canonical)
        if (index >= 0) {
          bundled[index] = {
            id: canonical,
            title: doc.title?.trim() || bundled[index].title,
            content_md: doc.content_md,
          }
        }
      } else if (doc.title?.trim()) {
        const index = bundled.findIndex((item) => item.id === canonical)
        if (index >= 0) {
          bundled[index] = { ...bundled[index], title: doc.title.trim() }
        }
      }
      continue
    }
    if (doc.id && doc.title?.trim()) {
      extras.push(doc)
    }
  }
  return [...bundled, ...extras]
}
