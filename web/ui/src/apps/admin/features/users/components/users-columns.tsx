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
// Modified 2026-11-06 by new-api-sub: portal user columns (ID, username, owner admin for super, status, two-step status, channel count, run/settled/pending USD, created time)
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { TimestampCell } from '@/components/activity-time-cell'
import { LongText } from '@/components/long-text'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { formatUsd } from '@/features/settlements/lib'
import { useAuthStore } from '@/stores/auth-store'

import { USER_STATUS, type PortalUser } from '../api'
import { DataTableRowActions } from './data-table-row-actions'

function pendingUsd(user: PortalUser): number | null {
  return user.used_usd === null ? null : user.used_usd - user.settled_usd
}

export function useUsersColumns(): ColumnDef<PortalUser>[] {
  const { t } = useTranslation()
  const isSuper = useAuthStore((s) => s.auth.user?.role === 'super')
  return useMemo<ColumnDef<PortalUser>[]>(
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
      ...(isSuper
        ? ([
            {
              accessorKey: 'admin_username',
              header: t('Owner Admin'),
              cell: ({ row }) => (
                <LongText className='max-w-[140px] text-sm font-normal'>
                  {row.original.admin_username}
                </LongText>
              ),
              size: 160,
              meta: { mobileOrder: 15 },
            },
          ] satisfies ColumnDef<PortalUser>[])
        : []),
      {
        accessorKey: 'status',
        header: t('Status'),
        cell: ({ row }) => {
          const enabled = row.original.status === USER_STATUS.ENABLED
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
        meta: { mobileOrder: 18 },
      },
      {
        accessorKey: 'channel_count',
        header: t('Channel Count'),
        cell: ({ row }) => (
          <span className='text-sm tabular-nums'>
            {row.original.channel_count}
          </span>
        ),
        size: 120,
        meta: { mobileOrder: 20 },
      },
      {
        accessorKey: 'used_usd',
        header: t('Run'),
        cell: ({ row }) => (
          <span className='text-sm tabular-nums'>
            {formatUsd(row.original.used_usd)}
          </span>
        ),
        size: 110,
        meta: { mobileOrder: 21 },
      },
      {
        accessorKey: 'settled_usd',
        header: t('Settled'),
        cell: ({ row }) => (
          <span className='text-sm tabular-nums'>
            {formatUsd(row.original.settled_usd)}
          </span>
        ),
        size: 110,
        meta: { mobileOrder: 22 },
      },
      {
        id: 'pending_usd',
        accessorFn: pendingUsd,
        header: t('Pending settlement'),
        cell: ({ row }) => (
          <span className='text-sm tabular-nums'>
            {formatUsd(pendingUsd(row.original))}
          </span>
        ),
        size: 110,
        meta: { mobileOrder: 23 },
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
    [t, isSuper]
  )
}
