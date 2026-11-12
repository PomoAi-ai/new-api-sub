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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  CONFLICT_KINDS,
  getPoolConflicts,
  getPools,
  poolsQueryKey,
  resolvePoolConflict,
  type Pool,
  type SyncConflict,
} from '../api'

const poolConflictsQueryKey = (poolId: number) =>
  ['admin-pool-conflicts', poolId] as const

/** Labels of the detail fields the sync records, as i18n keys. */
const DETAIL_LABELS: Record<string, string> = {
  created_at: 'Log time',
  request_id: 'Request ID',
  other_log_id: 'Log ID in the other pool',
  max_log_id: 'Largest log ID synced before',
  window_max_id: 'Largest log ID in this window',
  new_api_time: 'new-api time',
  now: 'Our time',
}
const DETAIL_TIMES = new Set(['created_at', 'new_api_time', 'now'])

const shortFingerprint = (fp: unknown) =>
  typeof fp === 'string' && fp ? fp.slice(0, 8) : '-'

function ConflictItem(props: {
  conflict: SyncConflict
  poolName: (id: number) => string | undefined
  busy: boolean
  onResolve: () => void
}) {
  const { conflict } = props
  const { t } = useTranslation()
  const kind = CONFLICT_KINDS[conflict.kind]
  const open = !conflict.resolved_at
  const { fp, stored_fp, other_pool_id, ...rest } = conflict.detail ?? {}
  const detail: { label: string; value: string; mono?: boolean }[] = []
  if (fp !== undefined || stored_fp !== undefined) {
    detail.push({
      label: t('Content fingerprint (synced / stored)'),
      value: `${shortFingerprint(fp)} / ${shortFingerprint(stored_fp)}`,
      mono: true,
    })
  }
  if (other_pool_id !== undefined) {
    detail.push({
      label: t('Other pool'),
      value:
        props.poolName(Number(other_pool_id)) ??
        t('Pool #{{id}}', { id: String(other_pool_id) }),
    })
  }
  for (const [key, value] of Object.entries(rest)) {
    const time = DETAIL_TIMES.has(key) && typeof value === 'number'
    detail.push({
      label: DETAIL_LABELS[key]
        ? t(DETAIL_LABELS[key])
        : t('Other detail ({{key}})', { key }),
      value: time
        ? formatTimestampToDate(value)
        : typeof value === 'string'
          ? value
          : JSON.stringify(value),
      mono: !time,
    })
  }
  return (
    <li className='space-y-2 py-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='font-medium'>
            {kind ? t(kind.label) : conflict.kind}
          </span>
          {open ? (
            <StatusBadge
              label={
                conflict.kind === 'clock_skew' ? t('Warning') : t('Unresolved')
              }
              variant={conflict.kind === 'clock_skew' ? 'warning' : 'danger'}
              size='sm'
              copyable={false}
            />
          ) : (
            <StatusBadge
              label={t('Resolved by {{name}}', { name: conflict.resolved_by })}
              variant='neutral'
              size='sm'
              copyable={false}
            />
          )}
        </div>
        {open && (
          <Button
            size='sm'
            variant='outline'
            disabled={props.busy}
            onClick={props.onResolve}
          >
            {props.busy && <Loader2 className='size-4 animate-spin' />}
            {t('Confirm and continue')}
          </Button>
        )}
      </div>
      {kind && (
        <p className='text-muted-foreground text-xs'>{t(kind.explain)}</p>
      )}
      <dl className='text-muted-foreground grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs'>
        {conflict.log_id > 0 && (
          <>
            <dt>{t('Log ID')}</dt>
            <dd className='font-mono'>{conflict.log_id}</dd>
          </>
        )}
        <dt>{t('Times seen')}</dt>
        <dd className='tabular-nums'>{conflict.count}</dd>
        <dt>{t('First seen')}</dt>
        <dd>{formatTimestampToDate(conflict.first_seen)}</dd>
        <dt>{t('Last seen')}</dt>
        <dd>{formatTimestampToDate(conflict.last_seen)}</dd>
        {detail.map(({ label, value, mono }) => (
          <div key={label} className='contents'>
            <dt>{label}</dt>
            <dd className={mono ? 'font-mono break-all' : 'break-all'}>
              {value}
            </dd>
          </div>
        ))}
      </dl>
    </li>
  )
}

/** The pool's sync conflicts, as the server lists them. */
export function PoolConflictsDialog(props: {
  pool: Pool
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { pool } = props
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [resolving, setResolving] = useState<number | null>(null)
  const query = useQuery({
    queryKey: poolConflictsQueryKey(pool.id),
    queryFn: () => getPoolConflicts(pool.id),
    enabled: props.open,
    meta: { errorToast: false, errorRedirect: false },
  })
  const pools = useQuery({
    queryKey: poolsQueryKey,
    queryFn: getPools,
    enabled: props.open,
    meta: { errorToast: false, errorRedirect: false },
  })
  const poolName = (id: number) => pools.data?.find((p) => p.id === id)?.name

  const resolve = async (conflict: SyncConflict) => {
    setResolving(conflict.id)
    try {
      const { resumed } = await resolvePoolConflict(pool.id, conflict.id)
      toast.success(
        resumed
          ? t('Conflict resolved. The sync resumes.')
          : t('Conflict resolved')
      )
    } catch (error) {
      handleServerError(error, t('Failed to resolve the conflict'))
    } finally {
      setResolving(null)
      void queryClient.invalidateQueries({
        queryKey: poolConflictsQueryKey(pool.id),
      })
      void queryClient.invalidateQueries({ queryKey: poolsQueryKey })
    }
  }

  let body = <LoadingState message={t('Loading...')} />
  if (query.isError) {
    body = (
      <ErrorState
        title={t('Failed to load the conflicts')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  } else if (query.data?.length === 0) {
    body = <EmptyState title={t('No conflicts')} />
  } else if (query.data) {
    body = (
      <ul className='divide-y'>
        {query.data.map((conflict) => (
          <ConflictItem
            key={conflict.id}
            conflict={conflict}
            poolName={poolName}
            busy={resolving !== null}
            onResolve={() => void resolve(conflict)}
          />
        ))}
      </ul>
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Sync conflicts of {{name}}', { name: pool.name })}
      description={t(
        'Logs that conflict are never overwritten. Confirming marks a conflict handled; the sync resumes once none that pauses it is left.'
      )}
    >
      {body}
    </Dialog>
  )
}
