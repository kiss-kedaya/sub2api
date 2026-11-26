import { describe, expect, it } from 'vitest'
import {
  TICKET_IMAGE_MAX_BYTES,
  extractClipboardImages,
  hasDraggedFiles,
  hasErroredImages,
  hasUploadingImages,
  readyAttachmentIDs,
  validateTicketImageFile,
  type PendingTicketImage
} from '../ticketImages'

function fakeFile(name: string, type: string, size: number): File {
  const file = new File(['x'], name, { type })
  Object.defineProperty(file, 'size', { value: size })
  return file
}

describe('ticketImages helpers', () => {
  it('validates allowed image types and size boundaries', () => {
    expect(validateTicketImageFile(fakeFile('a.png', 'image/png', 1024))).toBeNull()
    expect(validateTicketImageFile(fakeFile('a.jpg', 'image/jpeg', TICKET_IMAGE_MAX_BYTES))).toBeNull()
    expect(validateTicketImageFile(fakeFile('legacy.jpg', 'image/jpg', 512))).toBeNull()
    expect(validateTicketImageFile(fakeFile('a.webp', 'image/webp', 10))).toBeNull()
    expect(validateTicketImageFile(fakeFile('a.gif', 'image/gif', 10))).toBeNull()
    expect(validateTicketImageFile(fakeFile('xss.svg', 'image/svg+xml', 100))).toBe('type')
    expect(validateTicketImageFile(fakeFile('doc.pdf', 'application/pdf', 100))).toBe('type')
    expect(validateTicketImageFile(fakeFile('empty.png', 'image/png', 0))).toBe('size')
    expect(validateTicketImageFile(fakeFile('huge.png', 'image/png', TICKET_IMAGE_MAX_BYTES + 1))).toBe('size')
  })

  it('extracts image files from clipboard items including empty-MIME image filenames and ignores text items', () => {
    const png = fakeFile('paste.png', 'image/png', 128)
    const webpNoMime = fakeFile('clip.webp', '', 256)
    const event = {
      clipboardData: {
        items: [
          { kind: 'string', type: 'text/plain', getAsFile: () => null },
          { kind: 'file', type: 'image/png', getAsFile: () => png },
          { kind: 'file', type: '', getAsFile: () => webpNoMime }
        ],
        files: []
      }
    } as unknown as ClipboardEvent
    expect(extractClipboardImages(event)).toEqual([png, webpNoMime])
  })

  it('detects file drags without hijacking plain text drags', () => {
    expect(hasDraggedFiles({ dataTransfer: { types: ['Files'] } } as unknown as DragEvent)).toBe(true)
    expect(hasDraggedFiles({ dataTransfer: { types: ['text/plain'] } } as unknown as DragEvent)).toBe(false)
    expect(hasDraggedFiles({ dataTransfer: null } as unknown as DragEvent)).toBe(false)
  })

  it('tracks uploading, errored, and ready attachment IDs', () => {
    const items: PendingTicketImage[] = [
      { localID: '1', fileName: 'a.png', previewURL: '', status: 'uploading' },
      { localID: '2', fileName: 'b.png', previewURL: '', status: 'error', error: 'fail' },
      {
        localID: '3', fileName: 'c.png', previewURL: '', status: 'ready',
        attachment: { id: 42, file_name: 'c.png', content_type: 'image/png', size_bytes: 10, width: 1, height: 1, created_at: '' }
      }
    ]
    expect(hasUploadingImages(items)).toBe(true)
    expect(hasErroredImages(items)).toBe(true)
    expect(readyAttachmentIDs(items)).toEqual([42])
  })
})
