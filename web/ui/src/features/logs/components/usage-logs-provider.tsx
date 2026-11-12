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
// Modified 2026-11-04 by new-api-sub: this app's mode and role decide the view; every role has the administrator columns
/* eslint-disable react-refresh/only-export-components */
import { createContext, useContext, useState, type ReactNode } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import type { LogsMode } from '../types'

export type LogsViewScope = 'all' | 'self'
export type LogsViewAccess = 'admin' | 'root'

interface UsageLogsContextValue {
  mode: LogsMode
  sensitiveVisible: boolean
  setSensitiveVisible: (visible: boolean) => void
  viewScope: LogsViewScope
  setViewScope: (scope: LogsViewScope) => void
  /** The server only shows this portal user their own channels. */
  selfOnly: boolean
}

const UsageLogsContext = createContext<UsageLogsContextValue | undefined>(
  undefined
)

export function UsageLogsProvider({
  mode,
  children,
}: {
  mode: LogsMode
  children: ReactNode
}) {
  const [sensitiveVisible, setSensitiveVisible] = useState(true)
  const [viewScope, setViewScope] = useState<LogsViewScope>('all')
  const selfOnly = useAuthStore(
    (state) => mode === 'portal' && state.auth.user?.log_scope === 'self'
  )

  return (
    <UsageLogsContext.Provider
      value={{
        mode,
        sensitiveVisible,
        setSensitiveVisible,
        viewScope: selfOnly ? 'self' : viewScope,
        setViewScope,
        selfOnly,
      }}
    >
      {children}
    </UsageLogsContext.Provider>
  )
}

export function useUsageLogsContext() {
  const context = useContext(UsageLogsContext)
  if (!context) {
    throw new Error('useUsageLogsContext must be used within UsageLogsProvider')
  }
  return context
}

/**
 * Everyone sees the administrator's columns (`isAdminView`). Only a super
 * (`isRootView`) gets real values for the end user: the server sends anyone
 * else a row without them. "Only Mine" narrows the rows, not the columns.
 */
export function useLogsViewScope() {
  const { mode, viewScope, setViewScope, selfOnly } = useUsageLogsContext()
  const isSuper = useAuthStore((state) => state.auth.user?.role === 'super')
  const isRootView = mode === 'admin' && isSuper
  const viewAccess: LogsViewAccess = isRootView ? 'root' : 'admin'

  return {
    viewScope,
    setViewScope,
    selfOnly,
    isAdminView: true,
    isRootView,
    viewAccess,
  }
}
