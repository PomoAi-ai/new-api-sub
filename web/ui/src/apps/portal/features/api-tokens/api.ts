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
import { withProof } from '@/features/account/security/api'
import { api } from '@/lib/api'

export type ApiToken = {
  id: number
  name: string
  hint: string
  expires_at: number | null
  last_used_at: number | null
  created_at: number
}

export async function getApiTokens(): Promise<ApiToken[]> {
  return (await api.get<ApiToken[]>('/api/tokens')).data
}

/** The returned `token` is the only time the plaintext is available. */
export async function createApiToken(
  body: {
    name: string
    expires_at?: number
  },
  proof: string
): Promise<{ token: string }> {
  return (
    await api.post<{ token: string }>('/api/tokens', body, withProof(proof))
  ).data
}

export async function deleteApiToken(id: number): Promise<void> {
  await api.delete(`/api/tokens/${id}`)
}
