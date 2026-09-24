import { apiClient } from './client'
import type { BasePaginationResponse } from '@/types'

const clientIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

export function newClientID(): string {
  const cryptoObj = globalThis.crypto
  if (typeof cryptoObj?.randomUUID === 'function') return cryptoObj.randomUUID()
  const bytes = new Uint8Array(16)
  if (typeof cryptoObj?.getRandomValues === 'function') cryptoObj.getRandomValues(bytes)
  else for (let i = 0; i < bytes.length; i += 1) bytes[i] = Math.floor(Math.random() * 256)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export function isClientID(value: string): boolean {
  return clientIDPattern.test(value)
}

export const ticketStatuses = ['open', 'in_progress', 'waiting_user', 'resolved', 'closed'] as const
export const ticketCategories = ['billing', 'api', 'account', 'other'] as const
export const ticketPriorities = ['normal', 'high', 'urgent'] as const
export type TicketStatus = typeof ticketStatuses[number]
export type TicketCategory = typeof ticketCategories[number]
export type TicketPriority = typeof ticketPriorities[number]

export interface Ticket {
  id: number
  user_id: number
  subject: string
  contact: string
  category: TicketCategory
  priority: TicketPriority
  status: TicketStatus
  assignee_id: number | null
  assignee_name: string
  user_name: string
  user_email?: string
  created_at: string
  updated_at: string
  last_message_at: string
  last_message_preview: string
  last_message_id: number
  unread_count: number
}

export interface TicketMessage {
  id: number
  ticket_id: number
  author_id: number | null
  author_role: 'user' | 'admin' | 'system'
  author_name: string
  content: string
  kind: 'reply' | 'event'
  event_type?: string
  event_data?: { from?: unknown; to?: unknown }
  created_at: string
}

export interface TicketDetail {
  ticket: Ticket
  messages: TicketMessage[]
  has_more: boolean
  requester?: { id: number; username: string; email: string; balance: number; status: string; created_at: string }
}

export type TicketStats = Record<TicketStatus | 'total' | 'unread', number> & { can_create?: boolean; next_create_at?: string | null }
export interface TicketFilters {
  page: number
  page_size: number
  search?: string
  status?: TicketStatus | 'active' | ''
  category?: TicketCategory | ''
  priority?: TicketPriority | ''
  assigned_to?: 'all' | 'mine' | 'unassigned'
}
export interface CreateTicket {
  subject: string
  contact: string
  content: string
  category: TicketCategory
  priority: TicketPriority
  client_id: string
}
export interface TicketReply {
  content: string
  client_id: string
  status?: 'waiting_user' | 'resolved' | 'in_progress'
}
export interface TicketUpdate {
  status?: TicketStatus
  priority?: TicketPriority
  assigned_to?: 'me' | 'unassigned'
}

export function ticketsAPI(admin: boolean) {
  const base = admin ? '/admin/tickets' : '/tickets'
  return {
    async list(params: TicketFilters, signal?: AbortSignal) {
      return (await apiClient.get<BasePaginationResponse<Ticket>>(base, { params, signal })).data
    },
    async stats(signal?: AbortSignal) {
      return (await apiClient.get<TicketStats>(`${base}/stats`, { signal })).data
    },
    async detail(id: number, signal?: AbortSignal, before_id?: number) {
      return (await apiClient.get<TicketDetail>(`${base}/${id}`, { params: { before_id }, signal })).data
    },
    async create(input: CreateTicket) {
      return (await apiClient.post<TicketDetail>('/tickets', input)).data
    },
    async reply(id: number, input: TicketReply) {
      return (await apiClient.post<TicketDetail>(`${base}/${id}/replies`, input)).data
    },
    async update(id: number, input: TicketUpdate) {
      return (await apiClient.patch<TicketDetail>(`${base}/${id}`, input)).data
    },
    async read(id: number, last_message_id: number) {
      await apiClient.post(`${base}/${id}/read`, { last_message_id })
    }
  }
}
