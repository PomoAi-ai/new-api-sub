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
// Modified 2026-11-04 by new-api-sub: channel template columns, cells from users-columns.tsx and channels-columns.tsx
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { TruncatedCell } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import {
  ChannelGroupsCell,
  ChannelModelsCell,
  ChannelTypeCell,
} from '@/features/channels/components/channel-cells'

import type { ChannelTemplate } from '../api'
import { DataTableRowActions } from './data-table-row-actions'

export function useTemplatesColumns(): ColumnDef<ChannelTemplate>[] {
  const { t } = useTranslation()
  return useMemo<ColumnDef<ChannelTemplate>[]>(
    () => [
      {
        accessorKey: 'id',
        header: t('ID'),
        cell: ({ row }) => <TableId value={row.original.id} />,
        size: 80,
        meta: { mobileHidden: true },
      },
      {
        accessorKey: 'name',
        header: t('Name'),
        cell: ({ row }) => (
          <TruncatedCell className='max-w-[170px] font-medium'>
            {row.original.name}
          </TruncatedCell>
        ),
        enableHiding: false,
        size: 170,
        meta: { mobileTitle: true },
      },
      {
        accessorKey: 'channel_type',
        header: t('Type'),
        cell: ({ row }) => <ChannelTypeCell type={row.original.channel_type} />,
        size: 180,
      },
      {
        accessorKey: 'base_url',
        header: t('Base URL'),
        cell: ({ row }) => (
          <div className='flex max-w-[180px] min-w-0 flex-col gap-1'>
            <TruncatedCell className='max-w-full font-mono text-xs'>
              {row.original.base_url || '-'}
            </TruncatedCell>
            {row.original.allow_custom_base_url && (
              <StatusBadge
                label={t('Supplier may override')}
                variant='blue'
                size='sm'
                copyable={false}
                className='-ml-1.5 w-fit'
              />
            )}
          </div>
        ),
        size: 220,
        meta: { mobileHidden: true },
      },
      {
        accessorKey: 'models',
        header: t('Models'),
        cell: ({ row }) => (
          <div className='max-w-[250px] overflow-hidden'>
            <ChannelModelsCell models={row.original.models} />
          </div>
        ),
        size: 200,
        enableSorting: false,
        meta: { mobileHidden: true },
      },
      {
        accessorKey: 'group',
        header: t('Groups'),
        cell: ({ row }) => (
          <div className='min-w-[160px]'>
            <ChannelGroupsCell groups={row.original.group} />
          </div>
        ),
        size: 180,
        enableSorting: false,
        meta: { mobileHidden: true },
      },
      {
        accessorKey: 'priority',
        header: t('Priority'),
        cell: ({ row }) => (
          <span className='tabular-nums'>{row.original.priority}</span>
        ),
        size: 110,
        meta: { mobileHidden: true },
      },
      {
        accessorKey: 'weight',
        header: t('Weight'),
        cell: ({ row }) => (
          <span className='tabular-nums'>{row.original.weight}</span>
        ),
        size: 90,
        meta: { mobileHidden: true },
      },
      {
        accessorKey: 'enabled',
        header: t('Status'),
        cell: ({ row }) => (
          <StatusBadge
            label={row.original.enabled ? t('Enabled') : t('Disabled')}
            variant={row.original.enabled ? 'success' : 'neutral'}
            size='sm'
            copyable={false}
            className='-ml-1.5'
          />
        ),
        size: 100,
        meta: { mobileBadge: true },
      },
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => <DataTableRowActions row={row} />,
        enableSorting: false,
        enableHiding: false,
        meta: { pinned: 'right' as const },
      },
    ],
    [t]
  )
}
