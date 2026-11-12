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
// Modified 2026-11-06 by new-api-sub: admin columns (ID, username, role, status, two-step status, sub-user count, created time); from users/components/users-columns.tsx
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { TimestampCell } from '@/components/activity-time-cell'
import { LongText } from '@/components/long-text'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'

import { ADMIN_STATUS, type AdminAccount } from '../api'
import { DataTableRowActions } from './data-table-row-actions'

export function useAdminsColumns(): ColumnDef<AdminAccount>[] {
  const { t } = useTranslation()
  return useMemo<ColumnDef<AdminAccount>[]>(
    () => [
      {
        accessorKey: 'id',
        header: t('ID'),
        cell: ({ row }) => (
          <TableId
            value={row.original.id}
            className='w-[60px] [font-family:inherit] text-sm'
          />
        ),
        size: 80,
        meta: { mobileOrder: 10 },
      },
      {
        accessorKey: 'username',
        header: t('Username'),
        cell: ({ row }) => (
          <div className='flex min-w-[160px] flex-col gap-1'>
            <LongText className='max-w-[140px] text-sm font-normal'>
              {row.original.username}
            </LongText>
          </div>
        ),
        enableHiding: false,
        size: 220,
        meta: { mobileTitle: true },
      },
      {
        accessorKey: 'role',
        header: t('Role'),
        cell: ({ row }) => (
          <span className='text-sm'>
            {row.original.role === 'super' ? t('Super Admin') : t('Admin')}
          </span>
        ),
        filterFn: (row, id, value) => value.includes(String(row.getValue(id))),
        enableSorting: false,
        size: 120,
        meta: { mobileOrder: 20 },
      },
      {
        accessorKey: 'status',
        header: t('Status'),
        cell: ({ row }) => {
          const enabled = row.original.status === ADMIN_STATUS.ENABLED
          return (
            <div className='-ml-1.5'>
              <StatusBadge
                label={enabled ? t('Enabled') : t('Disabled')}
                variant={enabled ? 'success' : 'neutral'}
                copyable={false}
                className='font-normal'
              />
            </div>
          )
        },
        filterFn: (row, id, value) => value.includes(String(row.getValue(id))),
        enableSorting: false,
        size: 120,
        meta: { mobileBadge: true },
      },
      {
        accessorKey: 'mfa_enabled',
        header: t('Two-step verification'),
        cell: ({ row }) => (
          <div className='-ml-1.5'>
            <StatusBadge
              label={row.original.mfa_enabled ? t('Enabled') : t('Not set up')}
              variant={row.original.mfa_enabled ? 'success' : 'neutral'}
              copyable={false}
              className='font-normal'
            />
          </div>
        ),
        enableSorting: false,
        size: 130,
        meta: { mobileOrder: 25 },
      },
      {
        accessorKey: 'user_count',
        header: t('Sub-user Count'),
        cell: ({ row }) => (
          <span className='text-sm tabular-nums'>
            {row.original.user_count}
          </span>
        ),
        size: 120,
        meta: { mobileOrder: 30 },
      },
      {
        accessorKey: 'created_at',
        header: t('Created At'),
        cell: ({ row }) => (
          <TimestampCell
            timestamp={row.original.created_at}
            format='absolute'
            justNowLabel={t('Just now')}
            className='text-muted-foreground text-xs'
          />
        ),
        size: 200,
        meta: { mobileHidden: true },
      },
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => <DataTableRowActions row={row} />,
        meta: { pinned: 'right' as const },
      },
    ],
    [t]
  )
}
