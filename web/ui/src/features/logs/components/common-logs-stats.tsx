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
// Modified 2026-11-08 by new-api-sub: reads /api/logs/stat with the filter of the current view; notes rates that leave out synced pools
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { formatLogQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getLogStats } from '../api'
import { buildApiParams } from '../lib/utils'
import { useLogsViewScope, useUsageLogsContext } from './usage-logs-provider'

const route = getRouteApi('/_authenticated/logs/')

function StatBadge(props: {
  label: string
  value: string | number
  accent: string
  title?: string
}) {
  return (
    <span
      title={props.title}
      className='border-border/60 bg-muted/25 inline-flex h-7 items-center gap-2 rounded-md border px-2.5 text-xs shadow-xs'
    >
      <span className={cn('h-3.5 w-0.5 rounded-full', props.accent)} />
      <span className='text-muted-foreground'>{props.label}</span>
      <span className='text-foreground/85 font-mono font-semibold tabular-nums'>
        {props.value}
      </span>
    </span>
  )
}

export function CommonLogsStats() {
  const { t } = useTranslation()
  const { isAdminView: isAdmin, viewScope } = useLogsViewScope()
  const searchParams = route.useSearch()
  const { sensitiveVisible } = useUsageLogsContext()

  const { data: stats, isLoading } = useQuery({
    queryKey: ['usage-logs-stats', viewScope, searchParams],
    queryFn: () =>
      getLogStats(
        buildApiParams({
          page: 1,
          pageSize: 1,
          searchParams,
          columnFilters: [],
          isAdmin,
          viewScope,
        })
      ),
    placeholderData: (previousData) => previousData,
    // The list query fails with the same error and already shows it.
    meta: { errorToast: false },
  })

  // The server leaves synced pools, minutes behind, out of the rates.
  const ratesNote = Boolean(
    stats?.synced_pool_ids?.length || stats?.retention?.pool_ids.length
  )

  if (isLoading) {
    return (
      <div className='flex items-center gap-2'>
        <Skeleton className='h-7 w-[150px] rounded-md' />
        <Skeleton className='h-7 w-[100px] rounded-md' />
        <Skeleton className='h-7 w-[120px] rounded-md' />
        <Skeleton className='h-7 w-[90px] rounded-md' />
      </div>
    )
  }

  return (
    <div className='flex flex-wrap items-center gap-2'>
      <StatBadge
        label={t('Usage')}
        value={sensitiveVisible ? formatLogQuota(stats?.quota || 0) : '••••'}
        accent='bg-sky-500/70'
      />
      <StatBadge
        label={t('RPM')}
        value={stats?.rpm || 0}
        accent='bg-rose-500/65'
      />
      <StatBadge
        label={t('TPM')}
        value={stats?.tpm || 0}
        accent='bg-slate-400/70'
      />
      <StatBadge
        label={t('Concurrency')}
        value={stats?.concurrency || 0}
        accent='bg-amber-500/70'
        title={t('Most requests in flight at once in the last 60 seconds')}
      />
      {ratesNote && (
        <span className='text-muted-foreground text-xs'>
          {t(
            'RPM, TPM and concurrency leave out pools that sync their logs, which are minutes behind'
          )}
        </span>
      )}
    </div>
  )
}
