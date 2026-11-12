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
// Modified 2026-11-04 by new-api-sub: dialog state for admins (from users/components/users-provider.tsx)
import React, { useMemo, useState } from 'react'

import useDialogState from '@/hooks/use-dialog'

import type { AdminAccount } from '../api'

type AdminsDialogType = 'create' | 'delete' | 'password'

type AdminsContextType = {
  open: AdminsDialogType | null
  setOpen: (str: AdminsDialogType | null) => void
  currentRow: AdminAccount | null
  setCurrentRow: React.Dispatch<React.SetStateAction<AdminAccount | null>>
}

const AdminsContext = React.createContext<AdminsContextType | null>(null)

export function AdminsProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useDialogState<AdminsDialogType>(null)
  const [currentRow, setCurrentRow] = useState<AdminAccount | null>(null)

  const value = useMemo(
    () => ({ open, setOpen, currentRow, setCurrentRow }),
    [open, setOpen, currentRow]
  )

  return <AdminsContext value={value}>{children}</AdminsContext>
}

// eslint-disable-next-line react-refresh/only-export-components
export const useAdmins = () => {
  const context = React.useContext(AdminsContext)

  if (!context) {
    throw new Error('useAdmins has to be used within <AdminsContext>')
  }

  return context
}
