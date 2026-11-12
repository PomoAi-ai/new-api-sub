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
import { Radio } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { IconBadge } from '@/components/ui/icon-badge'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { getChannelQuotaData } from '@/features/dashboard/api'
import { buildQueryParams, getDefaultDays } from '@/features/dashboard/lib'
import type { DashboardFilters } from '@/features/dashboard/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota, formatTokens } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'

/**
 * What each channel consumed over the page's time range, biggest first; split
 * by pool, a row per pool a channel is in.
 */
export function ChannelConsumptionTable(props: { filters: DashboardFilters }) {
  const { t, i18n } = useTranslation()
  const { filters } = props
  const [splitPool, setSplitPool] = useState(false)
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
    queryKey: ['dashboard-channel-consumption', params, splitPool],
    queryFn: () => getChannelQuotaData(params, splitPool),
  })
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  let body = (
    <div className='space-y-2 p-4'>
      <Skeleton className='h-8 w-full' />
      <Skeleton className='h-8 w-full' />
    </div>
  )
  if (query.isError) {
    body = (
      <ErrorState
        className='min-h-0 p-4'
        title={t('Failed to load channel consumption')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  } else if (query.data?.length === 0) {
    body = (
      <EmptyState
        className='min-h-0 p-4'
        title={t('No channel consumption in this time range')}
      />
    )
  } else if (query.data) {
    body = (
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Channel')}</TableHead>
            {splitPool && <TableHead>{t('Pool')}</TableHead>}
            <TableHead className='text-right'>{t('Requests')}</TableHead>
            <TableHead className='text-right'>{t('Tokens')}</TableHead>
            <TableHead className='text-right'>{t('Consumption')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {query.data.map((row) => (
            <TableRow key={`${row.channel_id}-${row.pool_id ?? 0}`}>
              <TableCell className='max-w-64 truncate'>
                {row.channel_name || `#${row.channel_id}`}
              </TableCell>
              {splitPool && (
                <TableCell className='max-w-48 truncate'>
                  {row.pool_name || `#${row.pool_id}`}
                </TableCell>
              )}
              <TableCell className='text-right tabular-nums'>
                {formatNumber(row.count ?? 0, locale)}
              </TableCell>
              <TableCell className='text-right tabular-nums'>
                {formatTokens(row.token_used ?? 0)}
              </TableCell>
              <TableCell className='text-right tabular-nums'>
                {formatQuota(row.quota ?? 0)}
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
          <Radio />
        </IconBadge>
        <div className='text-sm font-semibold'>
          {t('Consumption by Channel')}
        </div>
        <div className='ml-auto flex items-center gap-2'>
          <Switch
            id='channel-consumption-split-pool'
            size='sm'
            checked={splitPool}
            onCheckedChange={setSplitPool}
          />
          <Label
            htmlFor='channel-consumption-split-pool'
            className='text-xs font-normal'
          >
            {t('Split by pool')}
          </Label>
        </div>
      </div>
      {body}
    </div>
  )
}
