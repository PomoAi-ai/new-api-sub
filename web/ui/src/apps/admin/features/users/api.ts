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
import type { Settlement, SettlementOverview } from '@/features/settlements/api'
import { api } from '@/lib/api'

export const USER_STATUS = { ENABLED: 1, DISABLED: 2 } as const

export type PortalUser = {
  id: number
  username: string
  status: number
  channel_count: number
  created_at: number
  admin_id?: number
  admin_username?: string
  /** Empty: follows the global setting. */
  log_scope: UserLogScope
  /** null when the read-only database could not be read; not 0. */
  used_usd: number | null
  settled_usd: number
  /** An authenticator or passkey is bound. */
  mfa_enabled: boolean
}

export type UserLogScope = '' | 'all' | 'self'

export const usersQueryKey = ['admin-users'] as const

export async function getUsers(): Promise<PortalUser[]> {
  return (await api.get<PortalUser[]>('/api/users')).data
}

export async function createUser(body: {
  username: string
  password: string
  /** Super only; other admins always own the users they create. */
  admin_id?: number
}): Promise<void> {
  await api.post('/api/users', body)
}

export async function setUserStatus(id: number, status: number) {
  await api.post(`/api/users/${id}/status`, { status })
}

export async function resetUserPassword(id: number, password: string) {
  await api.post(`/api/users/${id}/password`, { password })
}

export async function resetUserMfa(id: number) {
  await api.post(`/api/users/${id}/2fa/reset`)
}

export async function setUserLogScope(id: number, logScope: UserLogScope) {
  await api.post(`/api/users/${id}/log-scope`, { log_scope: logScope })
}

export const userSettlementsQueryKey = (id: number) =>
  ['admin-user-settlements', id] as const

export async function getUserSettlements(
  id: number
): Promise<SettlementOverview> {
  return (await api.get<SettlementOverview>(`/api/users/${id}/settlements`))
    .data
}

export async function addUserSettlement(
  id: number,
  body: { amount_usd: number; note: string }
): Promise<Settlement> {
  return (await api.post<Settlement>(`/api/users/${id}/settlements`, body)).data
}

export async function deleteUser(id: number) {
  await api.delete(`/api/users/${id}`)
}
