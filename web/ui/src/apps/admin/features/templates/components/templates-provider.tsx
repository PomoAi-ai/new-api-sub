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
// Modified 2026-11-04 by new-api-sub: dialog state for channel templates (from users/components/users-provider.tsx)
import React, { useMemo, useState } from 'react'

import useDialogState from '@/hooks/use-dialog'

import type { ChannelTemplate } from '../api'

type TemplatesDialogType = 'create' | 'update' | 'delete'

type TemplatesContextType = {
  open: TemplatesDialogType | null
  setOpen: (str: TemplatesDialogType | null) => void
  currentRow: ChannelTemplate | null
  setCurrentRow: React.Dispatch<React.SetStateAction<ChannelTemplate | null>>
}

const TemplatesContext = React.createContext<TemplatesContextType | null>(null)

export function TemplatesProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useDialogState<TemplatesDialogType>(null)
  const [currentRow, setCurrentRow] = useState<ChannelTemplate | null>(null)

  const value = useMemo(
    () => ({ open, setOpen, currentRow, setCurrentRow }),
    [open, setOpen, currentRow]
  )

  return <TemplatesContext value={value}>{children}</TemplatesContext>
}

// eslint-disable-next-line react-refresh/only-export-components
export const useTemplates = () => {
  const context = React.useContext(TemplatesContext)

  if (!context) {
    throw new Error('useTemplates has to be used within <TemplatesContext>')
  }

  return context
}
