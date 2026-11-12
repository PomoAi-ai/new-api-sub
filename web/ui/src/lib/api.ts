/*
Copyright (C) 2023-2026 QuantumNous

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

For commercial licensing, please contact support@quantumnous.com
*/
// Modified 2026-11-05 by new-api-sub: only cookie-session helpers; getStatus() adds the quota unit of GET /api/me
import { api } from '@/lib/http-client'
import { useSystemConfigStore } from '@/stores/system-config-store'

export {
  bootstrapAuthentication,
  clearAuthenticatedClientState,
  clearAuthentication,
  resolveAuthentication,
} from '@/lib/auth-session'
export { api }
export type { ApiRequestConfig } from '@/lib/http-client'

// ============================================================================
// System APIs
// ============================================================================

export async function getStatus(): Promise<Record<string, unknown>> {
  const { data } = await api.get<Record<string, unknown>>('/api/status', {
    skipAuthRefresh: true,
    skipErrorHandler: true,
  })
  // quota_per_unit comes from GET /api/me; echo it so the status mapping keeps it.
  const { quotaPerUnit } = useSystemConfigStore.getState().config.currency
  return { ...data, quota_per_unit: quotaPerUnit }
}
