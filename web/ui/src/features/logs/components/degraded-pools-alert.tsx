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
import { Info, TriangleAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { formatTimestampToDate } from '@/lib/format'

import { getPoolNames } from '../api'
import { usePoolNotices } from '../lib/pool-notices'
import type { DegradedPool } from '../types'

/** Pools left out of the latest responses under prefix, e.g. "logs:". */
export function DegradedPoolsAlert(props: { prefix: string }) {
  const { t } = useTranslation()
  const notices = usePoolNotices()
  const pools = new Map<number, DegradedPool>()
  for (const [key, notice] of Object.entries(notices)) {
    if (!key.startsWith(props.prefix)) continue
    for (const pool of notice.degraded ?? []) pools.set(pool.pool_id, pool)
  }
  if (pools.size === 0) return null

  const named = [...pools.values()].filter((pool) => pool.name)
  return (
    <Alert variant='destructive'>
      <TriangleAlert />
      <AlertTitle>
        {t('Some pools are unavailable. The figures may be incomplete.')}
      </AlertTitle>
      {named.length > 0 && (
        <AlertDescription>
          <ul>
            {named.map((pool) => (
              <li key={pool.pool_id}>
                {pool.name}
                {pool.reason && `: ${pool.reason}`}
              </li>
            ))}
          </ul>
        </AlertDescription>
      )}
    </Alert>
  )
}

/**
 * Synced pools keep 30 days of logs: says so when a response under prefix
 * reaches further back.
 */
export function RetentionAlert(props: { prefix: string }) {
  const { t } = useTranslation()
  const notices = usePoolNotices()
  const { data: names = [] } = useQuery({
    queryKey: ['pool-names'],
    queryFn: getPoolNames,
  })
  let since = 0
  const pools = new Set<number>()
  for (const [key, notice] of Object.entries(notices)) {
    if (!key.startsWith(props.prefix) || !notice.retention) continue
    since = Math.max(since, notice.retention.since)
    for (const id of notice.retention.pool_ids) pools.add(id)
  }
  if (pools.size === 0) return null

  return (
    <Alert>
      <Info />
      <AlertDescription>
        {t(
          'Synced pools keep only the last 30 days of logs; nothing before {{since}} is included: {{pools}}',
          {
            pools: [...pools]
              .map((id) => names.find((p) => p.id === id)?.name || `#${id}`)
              .join(', '),
            since: formatTimestampToDate(since),
          }
        )}
      </AlertDescription>
    </Alert>
  )
}
