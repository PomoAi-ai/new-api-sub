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
// Modified 2026-11-04 by new-api-sub: cookie session only; keep user, setUser, reset, bootstrapState; admin app role
import { create } from 'zustand'

export type AdminRole = 'super' | 'admin'

export interface AuthUser {
  id: number
  username: string
  /** Only the admin app's GET /api/me returns a role. */
  role?: AdminRole
  /** Only the portal's GET /api/me: what this user may see in the logs. */
  log_scope?: 'all' | 'self'
  /** Only the admin app's GET /api/me: empty until a super has set it. */
  portal_url?: string
  /** Only the admin app: the new-api connection is unusable, see /settings. */
  restricted?: boolean
  /** A verification factor (authenticator or passkey) is bound. */
  mfa_enabled?: boolean
}

export type AuthBootstrapState = 'idle' | 'checking' | 'complete'

interface AuthState {
  auth: {
    user: AuthUser | null
    bootstrapState: AuthBootstrapState
    setUser: (user: AuthUser | null) => void
    reset: (bootstrapState?: AuthBootstrapState) => void
  }
}

export const useAuthStore = create<AuthState>()((set) => ({
  auth: {
    user: null,
    bootstrapState: 'idle',
    setUser: (user) =>
      set((state) => ({
        ...state,
        auth: { ...state.auth, user, bootstrapState: 'complete' },
      })),
    reset: (bootstrapState = 'complete') =>
      set((state) => ({
        ...state,
        auth: { ...state.auth, user: null, bootstrapState },
      })),
  },
}))
