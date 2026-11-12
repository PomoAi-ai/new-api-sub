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
// Modified 2026-11-06 by new-api-sub: rewritten for cookie sessions (GET /api/me), no token refresh; restricted mode and two-step status
import type { QueryClient } from '@tanstack/react-query'
import axios from 'axios'

import { api } from '@/lib/http-client'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

type MeResponse = AuthUser & { quota_per_unit: number }

/** sessionStorage: this tab's super dismissed the two-step reminder. */
export const MFA_REMINDER_KEY = 'admin.mfa-reminder-dismissed'

let pending: Promise<AuthUser | null> | null = null

async function fetchMe(): Promise<AuthUser | null> {
  try {
    const { data } = await api.get<MeResponse>('/api/me', {
      skipAuthRefresh: true,
      skipErrorHandler: true,
    })
    const systemConfig = useSystemConfigStore.getState()
    systemConfig.setConfig({
      currency: {
        ...systemConfig.config.currency,
        quotaPerUnit: data.quota_per_unit,
      },
    })
    const user = {
      id: data.id,
      username: data.username,
      role: data.role,
      log_scope: data.log_scope,
      portal_url: data.portal_url,
      restricted: data.restricted,
      mfa_enabled: data.mfa_enabled,
    }
    useAuthStore.getState().auth.setUser(user)
    return user
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 401) {
      useAuthStore.getState().auth.reset()
      return null
    }
    throw error
  }
}

/** Resolve the signed-in user from memory, or from GET /api/me once. */
export function resolveAuthentication(): Promise<AuthUser | null> {
  const { user, bootstrapState } = useAuthStore.getState().auth
  if (user || bootstrapState === 'complete') return Promise.resolve(user)
  pending ??= fetchMe().finally(() => {
    pending = null
  })
  return pending
}

export const bootstrapAuthentication = resolveAuthentication

/** A new sign-in reminds again, even in the tab that dismissed it. */
export function clearMfaReminder(): void {
  try {
    sessionStorage.removeItem(MFA_REMINDER_KEY)
  } catch {
    // Storage is blocked: nothing was remembered, so nothing needs clearing.
  }
}

export function clearAuthentication(): void {
  useAuthStore.getState().auth.reset()
}

export function clearAuthenticatedClientState(queryClient: QueryClient): void {
  queryClient.clear()
  clearAuthentication()
}
