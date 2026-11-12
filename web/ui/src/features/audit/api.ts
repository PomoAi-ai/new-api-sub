/*
Copyright (C) 2026 new-api-sub contributors

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { api } from '@/lib/api'

/** 'admin' sees every actor; 'portal' is restricted to the signed-in user by the server. */
export type AuditMode = 'admin' | 'portal'

export interface AuditLog {
  id: number
  actor_type: 'portal' | 'admin'
  actor_id: number | null
  actor_name: string
  action: string
  target_type: string
  target_id: string
  success: boolean
  detail: Record<string, unknown> | null
  ip: string
  // Admin API only.
  forwarded_for?: string
  user_agent?: string
  created_at: number
}

export interface AuditFilters {
  p: number
  page_size: number
  actor_type?: 'portal' | 'admin'
  actor_name?: string
  action?: string
  start?: number
  end?: number
  /** 'true' or 'false' */
  success?: string
  category?: string
  /** Admin API only: a whole address, or the start of one. */
  ip?: string
  /** Admin API only: the caller's own actions. */
  owner?: 'self'
}

export async function getAuditLogs(
  params: AuditFilters
): Promise<{ items: AuditLog[]; total: number }> {
  return (await api.get('/api/audit-logs', { params })).data
}
