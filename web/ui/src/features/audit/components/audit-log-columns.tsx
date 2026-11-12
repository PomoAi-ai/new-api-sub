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
// Modified 2026-11-04 by new-api-sub: this project's rows; the portal has no username or client (it only shows the caller's own records), and an address in the admin app filters by itself when clicked
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { TruncatedCell } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import dayjs from '@/lib/dayjs'

import type { AuditLog, AuditMode } from '../api'
import { auditEventText, auditRequest } from '../lib/audit-event'
import { AuditLogDetailsDialog } from './audit-log-details-dialog'

export function useAuditLogColumns(
  mode: AuditMode,
  onFilterIp: (ip: string) => void
): ColumnDef<AuditLog>[] {
  const { t } = useTranslation()
  return useMemo(() => {
    const isAdmin = mode === 'admin'
    const columns: ColumnDef<AuditLog>[] = [
      {
        accessorKey: 'created_at',
        header: t('Time'),
        size: 180,
        cell: ({ row }) => (
          <span className='font-mono tabular-nums'>
            {dayjs.unix(row.original.created_at).format('YYYY-MM-DD HH:mm:ss')}
          </span>
        ),
        meta: { label: t('Time'), mobileTitle: true },
      },
    ]
    if (isAdmin) {
      columns.push({
        accessorKey: 'actor_name',
        header: t('Username'),
        size: 100,
        meta: { label: t('Username') },
      })
    }
    columns.push(
      {
        id: 'event',
        header: t('Event'),
        size: 360,
        accessorFn: (entry) => auditEventText(entry, t),
        cell: ({ row, getValue }) => {
          const error = row.original.detail?.error
          return (
            <div className='min-w-0 space-y-1'>
              <TruncatedCell
                className='min-w-0 font-medium'
                tooltipContent={row.original.action}
              >
                {getValue<string>()}
              </TruncatedCell>
              {typeof error === 'string' && error && (
                <TruncatedCell
                  className='text-muted-foreground'
                  contentClassName='line-clamp-2 whitespace-normal break-words'
                  tooltipContent={error}
                >
                  {error}
                </TruncatedCell>
              )}
            </div>
          )
        },
        meta: { label: t('Event') },
      },
      {
        accessorKey: 'ip',
        header: 'IP',
        size: 120,
        cell: ({ row }) => {
          const ip = row.original.ip
          if (!isAdmin || !ip) {
            return <span className='font-mono'>{ip || '—'}</span>
          }
          return (
            <button
              type='button'
              className='font-mono hover:underline'
              title={t('Filter by this IP')}
              onClick={() => onFilterIp(ip)}
            >
              {ip}
            </button>
          )
        },
        meta: { label: 'IP' },
      }
    )
    if (isAdmin) {
      columns.push({
        accessorKey: 'user_agent',
        header: t('Client'),
        size: 180,
        cell: ({ row }) => (
          <TruncatedCell className='max-w-48'>
            {row.original.user_agent || '—'}
          </TruncatedCell>
        ),
        meta: { label: t('Client'), mobileHidden: true },
      })
    }
    columns.push(
      {
        id: 'method',
        header: t('Method'),
        size: 76,
        cell: ({ row }) => (
          <span className='text-muted-foreground font-mono'>
            {auditRequest(row.original).method || '—'}
          </span>
        ),
        meta: { label: t('Method') },
      },
      {
        id: 'route',
        header: t('Route'),
        size: 220,
        cell: ({ row }) => (
          <TruncatedCell className='max-w-60 font-mono'>
            {auditRequest(row.original).route || '—'}
          </TruncatedCell>
        ),
        meta: { label: t('Route') },
      },
      {
        id: 'status',
        header: 'HTTP',
        size: 60,
        cell: ({ row }) => (
          <span className='font-mono tabular-nums'>
            {auditRequest(row.original).status || '—'}
          </span>
        ),
        meta: { label: 'HTTP' },
      },
      {
        accessorKey: 'success',
        header: t('Result'),
        size: 82,
        cell: ({ row }) => (
          <StatusBadge
            label={row.original.success ? t('Success') : t('Failed')}
            variant={row.original.success ? 'success' : 'danger'}
            copyable={false}
          />
        ),
        meta: { label: t('Result'), mobileBadge: true },
      },
      {
        id: 'details',
        header: t('Details'),
        size: 70,
        enableHiding: false,
        cell: ({ row }) => (
          <AuditLogDetailsDialog entry={row.original} mode={mode} />
        ),
        meta: { label: t('Details') },
      }
    )
    return columns
  }, [mode, onFilterIp, t])
}
