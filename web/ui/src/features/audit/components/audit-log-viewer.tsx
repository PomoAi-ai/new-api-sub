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
// Modified 2026-11-04 by new-api-sub: page-number pagination over GET /api/audit-logs ({items,total}); admin/portal modes; typed filters wait for a pause, as each search is audited
import { useQuery } from '@tanstack/react-query'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useDebounce } from '@/hooks'
import { useAuthStore } from '@/stores/auth-store'

import {
  getAuditLogs,
  type AuditFilters,
  type AuditLog,
  type AuditMode,
} from '../api'
import { useAuditLogColumns } from './audit-log-columns'
import { AuditLogFilterBar } from './audit-log-filter-bar'

const EMPTY_LOGS: AuditLog[] = []

export function AuditLogViewer(props: {
  mode: AuditMode
  scope: 'all' | 'self'
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [filters, setFilters] = useState<AuditFilters>({ p: 1, page_size: 20 })
  const invalidRange =
    filters.start !== undefined &&
    filters.end !== undefined &&
    filters.start > filters.end
  const actorName = useDebounce(filters.actor_name)
  const ip = useDebounce(filters.ip)
  const params: AuditFilters = {
    ...filters,
    actor_name: actorName,
    ip,
    owner: props.scope === 'self' ? 'self' : undefined,
  }
  const query = useQuery({
    queryKey: ['audit', userId, props.mode, params],
    queryFn: () => getAuditLogs(params),
    enabled: !invalidRange,
    retry: false,
  })
  const filterByIp = useCallback(
    (address: string) =>
      setFilters((previous) => ({ ...previous, ip: address, p: 1 })),
    []
  )
  const columns = useAuditLogColumns(props.mode, filterByIp)
  const { table } = useDataTable({
    columns,
    data: query.isError ? EMPTY_LOGS : (query.data?.items ?? EMPTY_LOGS),
    getRowId: (entry) => String(entry.id),
    totalCount: query.isError ? 0 : (query.data?.total ?? 0),
    pagination: { pageIndex: filters.p - 1, pageSize: filters.page_size },
    onPaginationChange: (updater) => {
      if (query.isFetching || query.isError || invalidRange) return
      setFilters((previous) => {
        const current = {
          pageIndex: previous.p - 1,
          pageSize: previous.page_size,
        }
        const next = typeof updater === 'function' ? updater(current) : updater
        return {
          ...previous,
          p: next.pageSize === previous.page_size ? next.pageIndex + 1 : 1,
          page_size: next.pageSize,
        }
      })
    },
    enableRowSelection: false,
    enableSorting: false,
    manualFiltering: true,
    manualPagination: true,
  })
  const update = (patch: Partial<AuditFilters>) =>
    setFilters((previous) => ({ ...previous, ...patch, p: 1 }))

  return (
    <div className='flex h-full min-h-0 flex-col'>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={query.isPending && !invalidRange}
        isFetching={query.isFetching}
        emptyTitle={
          query.isError ? t('Failed to load audit records') : t('No records')
        }
        paginationInFooter
        className='h-auto min-h-0 flex-1'
        applyHeaderSize
        getColumnClassName={() => 'py-2'}
        tableClassName='[&_[data-slot=table]]:text-[13px] [&_[data-slot=table]_td]:text-[13px] [&_[data-slot=table]_td_*]:text-[13px] [&_[data-slot=table]_th]:text-[13px] [&_[data-slot=table]_th_*]:text-[13px]'
        toolbar={
          <div className='shrink-0 space-y-2'>
            <AuditLogFilterBar
              table={table}
              mode={props.mode}
              filters={filters}
              onChange={update}
              isFetching={query.isFetching}
              onSearch={() => {
                if (!invalidRange) void query.refetch()
              }}
              onReset={() => setFilters({ p: 1, page_size: filters.page_size })}
            />
            {invalidRange && (
              <Alert variant='destructive'>
                <AlertDescription>
                  {t('End time must be after start time')}
                </AlertDescription>
              </Alert>
            )}
            {query.isError && (
              <Alert variant='destructive'>
                <AlertDescription className='flex items-center justify-between gap-2'>
                  <span>{t('Failed to load audit records')}</span>
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={() => void query.refetch()}
                  >
                    {t('Retry')}
                  </Button>
                </AlertDescription>
              </Alert>
            )}
          </div>
        }
      />
    </div>
  )
}
