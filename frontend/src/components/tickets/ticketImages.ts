import type { TicketAttachment } from '@/api/tickets'

export const TICKET_IMAGE_MAX_BYTES = 5 * 1024 * 1024
export const TICKET_IMAGE_MAX_COUNT = 4
export const TICKET_IMAGE_MIME_TYPES = ['image/png', 'image/jpeg', 'image/webp', 'image/gif'] as const
export const TICKET_IMAGE_ACCEPT = TICKET_IMAGE_MIME_TYPES.join(',')

export function isSupportedTicketImageType(type: string): boolean {
  const normalized = type.trim().toLowerCase()
  return normalized === 'image/jpg' || (TICKET_IMAGE_MIME_TYPES as readonly string[]).includes(normalized)
}

export interface PendingTicketImage {
  localID: string
  fileName: string
  previewURL: string
  status: 'uploading' | 'ready' | 'error'
  attachment?: TicketAttachment
  error?: string
}

const allowedExtensions = /\.(png|jpe?g|webp|gif)$/i

export function isTicketAbortError(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const { name, code } = error as { name?: string; code?: string }
  return name === 'AbortError' || name === 'CanceledError' || code === 'ERR_CANCELED'
}

export function hasDraggedFiles(event: DragEvent): boolean {
  const types = event.dataTransfer?.types
  if (!types) return false
  return Array.from(types).includes('Files')
}

export function createObjectURLSafe(blob: Blob): string {
  if (typeof URL !== 'undefined' && typeof URL.createObjectURL === 'function') {
    return URL.createObjectURL(blob)
  }
  return ''
}

export function revokeObjectURLSafe(url: string): void {
  if (url && url.startsWith('blob:') && typeof URL !== 'undefined' && typeof URL.revokeObjectURL === 'function') {
    URL.revokeObjectURL(url)
  }
}

export function validateTicketImageFile(file: File): 'type' | 'size' | null {
  const type = (file.type || '').trim().toLowerCase()
  if (type) {
    if (!isSupportedTicketImageType(type)) return 'type'
  } else if (!allowedExtensions.test(file.name || '')) {
    return 'type'
  }
  if (file.size <= 0 || file.size > TICKET_IMAGE_MAX_BYTES) return 'size'
  return null
}

function isLikelyImageFile(file: File): boolean {
  const type = (file.type || '').trim().toLowerCase()
  if (type) return type.startsWith('image/')
  return allowedExtensions.test(file.name || '')
}

export function extractClipboardImages(event: ClipboardEvent): File[] {
  const data = event.clipboardData
  if (!data) return []
  const files: File[] = []
  if (data.items && data.items.length > 0) {
    for (let i = 0; i < data.items.length; i += 1) {
      const item = data.items[i]
      if (item.kind !== 'file') continue
      const type = (item.type || '').trim().toLowerCase()
      if (type && !type.startsWith('image/')) continue
      const file = item.getAsFile()
      if (file && isLikelyImageFile(file)) files.push(file)
    }
  }
  if (files.length === 0 && data.files && data.files.length > 0) {
    for (let i = 0; i < data.files.length; i += 1) {
      const file = data.files[i]
      if (isLikelyImageFile(file)) files.push(file)
    }
  }
  return files
}

export function hasUploadingImages(items: readonly PendingTicketImage[]): boolean {
  return items.some((item) => item.status === 'uploading')
}

export function hasErroredImages(items: readonly PendingTicketImage[]): boolean {
  return items.some((item) => item.status === 'error')
}

export function readyAttachmentIDs(items: readonly PendingTicketImage[]): number[] {
  return items
    .filter((item): item is PendingTicketImage & { attachment: TicketAttachment } => item.status === 'ready' && !!item.attachment)
    .map((item) => item.attachment.id)
}

export function clearPendingImages(items: PendingTicketImage[]): void {
  for (const item of items) revokeObjectURLSafe(item.previewURL)
  items.splice(0, items.length)
}
