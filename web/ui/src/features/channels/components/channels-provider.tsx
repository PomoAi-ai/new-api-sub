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
// Modified 2026-11-05 by new-api-sub: mode prop and create/test/balance/key dialogs only (no tag mode, batch mode, drag or upstream updates)
/* eslint-disable react-refresh/only-export-components */
import type { OnChangeFn, SortingState } from '@tanstack/react-table'
import React, {
  createContext,
  useContext,
  useState,
  useMemo,
  useEffect,
} from 'react'

import type { Channel, ChannelsMode } from '../types'

// ============================================================================
// Types
// ============================================================================

type DialogType =
  | 'create-channel'
  | 'edit-channel'
  | 'test-channel'
  | 'balance-query'
  | 'manage-keys'
  | null

type ChannelsContextType = {
  mode: ChannelsMode
  open: DialogType
  setOpen: (open: DialogType) => void
  currentRow: Channel | null
  setCurrentRow: (row: Channel | null) => void
  sorting: SortingState
  setSorting: OnChangeFn<SortingState>
  sensitiveVisible: boolean
  setSensitiveVisible: (visible: boolean) => void
}

// ============================================================================
// Context
// ============================================================================

const ChannelsContext = createContext<ChannelsContextType | undefined>(
  undefined
)

// ============================================================================
// Provider
// ============================================================================

export function ChannelsProvider(props: {
  mode: ChannelsMode
  children: React.ReactNode
}) {
  const [open, setOpen] = useState<DialogType>(null)
  const [currentRow, setCurrentRow] = useState<Channel | null>(null)
  const [sorting, setSorting] = useState<SortingState>(() => [
    {
      id:
        localStorage.getItem('channels-id-sort') === 'true' ? 'id' : 'priority',
      desc: true,
    },
  ])
  const idSort = sorting[0]?.id === 'id'
  useEffect(() => {
    // Retain the existing ID-sort preference for returning users.
    localStorage.setItem('channels-id-sort', String(idSort))
  }, [idSort])
  const [sensitiveVisible, setSensitiveVisible] = useState(true)

  // useState setters are stable, so the context value only needs to change when
  // an actual state value changes. Memoizing avoids handing every consumer
  // (including all channel cards/cells) a brand-new object on each render.
  const value = useMemo<ChannelsContextType>(
    () => ({
      mode: props.mode,
      open,
      setOpen,
      currentRow,
      setCurrentRow,
      sorting,
      setSorting,
      sensitiveVisible,
      setSensitiveVisible,
    }),
    [props.mode, open, currentRow, sorting, sensitiveVisible]
  )

  return (
    <ChannelsContext.Provider value={value}>
      {props.children}
    </ChannelsContext.Provider>
  )
}

// ============================================================================
// Hook
// ============================================================================

export function useChannels() {
  const context = useContext(ChannelsContext)
  if (!context) {
    throw new Error('useChannels must be used within ChannelsProvider')
  }
  return context
}
