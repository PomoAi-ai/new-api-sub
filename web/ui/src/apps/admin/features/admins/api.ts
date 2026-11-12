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
import type { AdminRole } from '@/stores/auth-store'

export const ADMIN_STATUS = { ENABLED: 1, DISABLED: 2 } as const

export type AdminAccount = {
  id: number
  username: string
  role: AdminRole
  status: number
  user_count: number
  created_at: number
  /** An authenticator or passkey is bound. */
  mfa_enabled: boolean
}

export const adminsQueryKey = ['admin-admins'] as const

export async function getAdmins(): Promise<AdminAccount[]> {
  return (await api.get<AdminAccount[]>('/api/admins')).data
}

export async function createAdmin(body: {
  username: string
  password: string
}): Promise<void> {
  await api.post('/api/admins', body)
}

export async function setAdminStatus(id: number, status: number) {
  await api.post(`/api/admins/${id}/status`, { status })
}

export async function resetAdminPassword(id: number, password: string) {
  await api.post(`/api/admins/${id}/password`, { password })
}

export async function resetAdminMfa(id: number) {
  await api.post(`/api/admins/${id}/2fa/reset`)
}

export async function deleteAdmin(id: number) {
  await api.delete(`/api/admins/${id}`)
}
