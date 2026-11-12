/*
Copyright (C) 2026 new-api-sub contributors

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
*/
import { useQuery } from '@tanstack/react-query'
import { Layers } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { getPoolQuotaData } from '@/features/dashboard/api'
import { buildQueryParams, getDefaultDays } from '@/features/dashboard/lib'
import type { DashboardFilters } from '@/features/dashboard/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota, formatTokens } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'

/**
 * What each pool served of the caller's channels over the page's time range,
 * biggest first. Quota is already in the home pool's units.
 */
export function PoolConsumptionTable(props: { filters: DashboardFilters }) {
  const { t, i18n } = useTranslation()
  const { filters } = props
  const params = useMemo(
    () =>
      buildQueryParams(
        computeTimeRange(
          getDefaultDays(filters.time_granularity),
          filters.start_timestamp,
          filters.end_timestamp
        ),
        filters
      ),
    [filters]
  )
  const query = useQuery({
    queryKey: ['dashboard-pool-consumption', params],
    queryFn: () => getPoolQuotaData(params),
  })
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const total = (query.data ?? []).reduce((sum, row) => sum + row.quota, 0)

  let body = (
    <div className='space-y-2 p-4'>
      <Skeleton className='h-8 w-full' />
    </div>
  )
  if (query.isError) {
    body = (
      <ErrorState
        className='min-h-0 p-4'
        title={t('Failed to load pool consumption')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  } else if (query.data?.length === 0) {
    body = (
      <EmptyState
        className='min-h-0 p-4'
        title={t('No pool consumption in this time range')}
      />
    )
  } else if (query.data) {
    body = (
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Pool')}</TableHead>
            <TableHead className='text-right'>{t('Requests')}</TableHead>
            <TableHead className='text-right'>{t('Tokens')}</TableHead>
            <TableHead className='text-right max-sm:hidden'>
              {t('Cache Read')}
            </TableHead>
            <TableHead className='text-right'>{t('Consumption')}</TableHead>
            <TableHead className='text-right'>{t('Share')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {query.data.map((row) => (
            <TableRow key={row.pool_id}>
              <TableCell className='max-w-64 truncate'>
                {row.pool_name || `#${row.pool_id}`}
              </TableCell>
              <TableCell className='text-right tabular-nums'>
                {formatNumber(row.count, locale)}
              </TableCell>
              <TableCell className='text-right tabular-nums'>
                {formatTokens(row.token_used)}
              </TableCell>
              <TableCell className='text-right tabular-nums max-sm:hidden'>
                {formatTokens(row.cache_read_tokens)}
              </TableCell>
              <TableCell className='text-right tabular-nums'>
                {formatQuota(row.quota)}
              </TableCell>
              <TableCell className='text-muted-foreground text-right tabular-nums'>
                {total > 0 ? `${((row.quota / total) * 100).toFixed(1)}%` : '-'}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    )
  }

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex items-center gap-2 border-b px-3 py-2 sm:px-5 sm:py-3'>
        <IconBadge tone='info' size='sm'>
          <Layers />
        </IconBadge>
        <div className='text-sm font-semibold'>{t('Consumption by Pool')}</div>
      </div>
      {body}
    </div>
  )
}
