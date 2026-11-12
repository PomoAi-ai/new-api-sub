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
import { useQueryClient } from '@tanstack/react-query'
import { Loader2, TriangleAlert } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { ProofCancelled } from '@/features/account/security/api'
import { useProof } from '@/features/account/security/use-proof'
import { formatQuota, formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import {
  CONFLICT_KINDS,
  poolsQueryKey,
  resetPoolSync,
  updatePoolSync,
  verifyPoolSync,
  type Pool,
  type PoolSync,
  type PoolSyncVerify,
} from '../api'
import { PoolConflictsDialog } from './pool-conflicts-dialog'

/** A cursor further behind than this is flagged. */
const LAG_WARN_S = 600

// The server's CHECK ranges; the backfill is that of a sync that never ran.
const FIELDS = [
  { key: 'interval_s', label: 'Interval (seconds)', min: 30, max: 3600 },
  { key: 'window_s', label: 'Window (seconds)', min: 60, max: 3600 },
  { key: 'max_windows', label: 'Windows per round', min: 1, max: 60 },
  { key: 'backfill_days', label: 'Backfill (days)', min: 0, max: 30 },
] as const
type FieldKey = (typeof FIELDS)[number]['key']

const DEFAULTS: Record<FieldKey, number> = {
  interval_s: 60,
  window_s: 600,
  max_windows: 6,
  backfill_days: 1,
}

function syncState(sync: PoolSync | null): {
  label: string
  variant: StatusVariant
} {
  if (!sync?.enabled) return { label: 'Sync off', variant: 'neutral' }
  if (sync.paused) return { label: 'Sync paused', variant: 'danger' }
  if ((sync.backoff_until ?? 0) * 1000 > Date.now()) {
    return { label: 'Backing off', variant: 'warning' }
  }
  return { label: 'Syncing', variant: 'success' }
}

/** State and counters of the pool's log sync. */
function SyncMonitor({ sync }: { sync: PoolSync }) {
  const { t } = useTranslation()
  const lagging = (sync.lag_s ?? 0) > LAG_WARN_S
  const paused = sync.paused ? CONFLICT_KINDS[sync.paused] : undefined
  const rows: [string, string][] = [
    [t('Synced up to'), formatTimestampToDate(sync.cursor ?? 0)],
    [t('Newest log'), formatTimestampToDate(sync.newest_log_at ?? 0)],
    [t('Last run'), formatTimestampToDate(sync.last_run_at ?? 0)],
    [t('Last success'), formatTimestampToDate(sync.last_ok_at ?? 0)],
    [
      t('Last round'),
      t('{{rows}} logs in {{ms}} ms', {
        rows: sync.last_rows,
        ms: sync.last_ms,
      }),
    ],
    [t('Logs synced'), String(sync.rows_total)],
  ]
  if ((sync.backoff_until ?? 0) * 1000 > Date.now()) {
    rows.push([
      t('Backing off until'),
      formatTimestampToDate(sync.backoff_until ?? 0),
    ])
  }
  return (
    <div className='space-y-2'>
      <dl className='grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm'>
        <dt className='text-muted-foreground'>{t('Behind by')}</dt>
        <dd
          className={cn('tabular-nums', lagging && 'text-warning font-medium')}
        >
          {sync.lag_s == null
            ? '-'
            : t('{{count}} seconds', { count: sync.lag_s })}
          {lagging && ` · ${t('More than 10 minutes behind')}`}
        </dd>
        {rows.map(([label, value]) => (
          <div key={label} className='contents'>
            <dt className='text-muted-foreground'>{label}</dt>
            <dd className='tabular-nums'>{value}</dd>
          </div>
        ))}
      </dl>
      {paused && (
        <Alert variant='destructive'>
          <TriangleAlert />
          <AlertDescription>
            {t('Paused: {{reason}}. Handle the conflicts to resume.', {
              reason: t(paused.label),
            })}
          </AlertDescription>
        </Alert>
      )}
      {sync.last_error && (
        <p className='text-destructive text-xs'>{sync.last_error}</p>
      )}
    </div>
  )
}

function VerifyResult({ result }: { result: PoolSyncVerify }) {
  const { t } = useTranslation()
  const same =
    result.db.count === result.mongo.count &&
    result.db.quota === result.mongo.quota
  return (
    <div className='space-y-1 rounded-lg border p-3 text-sm'>
      <div className='flex items-center gap-2'>
        <span>
          {formatTimestampToDate(result.start)} –{' '}
          {formatTimestampToDate(result.end)}
        </span>
        <StatusBadge
          label={same ? t('Matches') : t('Differs')}
          variant={same ? 'success' : 'warning'}
          size='sm'
          copyable={false}
        />
      </div>
      <dl className='grid grid-cols-[auto_1fr_1fr] gap-x-4 tabular-nums'>
        <dt />
        <dd className='text-muted-foreground'>{t('Requests')}</dd>
        <dd className='text-muted-foreground'>{t('Consumption')}</dd>
        <dt className='text-muted-foreground'>{t('Database')}</dt>
        <dd>{result.db.count}</dd>
        <dd>{formatQuota(result.db.quota)}</dd>
        <dt className='text-muted-foreground'>MongoDB</dt>
        <dd>{result.mongo.count}</dd>
        <dd>{formatQuota(result.mongo.quota)}</dd>
      </dl>
      {!same && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Logs of the last few minutes may not be synced yet. A difference that stays means logs were missed or the pools overlap.'
          )}
        </p>
      )}
    </div>
  )
}

/**
 * Controls and monitor of one pool's log sync, for a super. Settings apply
 * from the next round without a restart.
 */
export function PoolSyncPanel({ pool }: { pool: Pool }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { ask, dialog: proofDialog } = useProof()
  const sync = pool.sync
  const [enabled, setEnabled] = useState(sync?.enabled ?? false)
  const [values, setValues] = useState<Record<FieldKey, string>>(() => ({
    interval_s: String(sync?.interval_s ?? DEFAULTS.interval_s),
    window_s: String(sync?.window_s ?? DEFAULTS.window_s),
    max_windows: String(sync?.max_windows ?? DEFAULTS.max_windows),
    backfill_days: String(DEFAULTS.backfill_days),
  }))
  const [busy, setBusy] = useState<'save' | 'verify' | 'reset' | null>(null)
  const [verified, setVerified] = useState<PoolSyncVerify | null>(null)
  const [confirmReset, setConfirmReset] = useState(false)
  const [conflictsOpen, setConflictsOpen] = useState(false)

  const numbers = Object.fromEntries(
    FIELDS.map((f) => [f.key, Number(values[f.key])])
  ) as Record<FieldKey, number>
  const invalid = FIELDS.filter(
    (f) =>
      values[f.key].trim() === '' ||
      !Number.isInteger(numbers[f.key]) ||
      numbers[f.key] < f.min ||
      numbers[f.key] > f.max
  ).map((f) => f.key)
  const state = syncState(sync)
  const refresh = () =>
    void queryClient.invalidateQueries({ queryKey: poolsQueryKey })

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setBusy('save')
    try {
      await updatePoolSync(pool.id, { enabled, ...numbers })
      toast.success(t('Sync settings saved'))
      refresh()
    } catch (error) {
      handleServerError(error, t('Failed to save the sync settings'))
    } finally {
      setBusy(null)
    }
  }

  const verify = async () => {
    setBusy('verify')
    setVerified(null)
    try {
      setVerified(await verifyPoolSync(pool.id, 24))
    } catch (error) {
      handleServerError(error, t('Failed to verify the sync'))
    } finally {
      setBusy(null)
    }
  }

  const reset = async () => {
    setConfirmReset(false)
    setBusy('reset')
    try {
      const proof = await ask('newapi_config')
      await resetPoolSync(pool.id, numbers.backfill_days, proof)
      toast.success(t('Synced logs cleared. The sync starts over.'))
      refresh()
    } catch (error) {
      if (!(error instanceof ProofCancelled)) {
        handleServerError(error, t('Failed to clear and resync'))
      }
    } finally {
      setBusy(null)
    }
  }

  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center gap-2'>
        <StatusBadge
          label={t(state.label)}
          variant={state.variant}
          size='sm'
          copyable={false}
        />
        {sync && sync.open_conflicts > 0 && (
          <StatusBadge
            label={t('{{count}} open conflicts', {
              count: sync.open_conflicts,
            })}
            variant='danger'
            size='sm'
            copyable={false}
          />
        )}
      </div>
      {pool.log_mode === 'sync' && !enabled && (
        <Alert variant='destructive'>
          <TriangleAlert />
          <AlertDescription>
            {t(
              'This pool reads its logs from MongoDB: with the sync off, its logs and statistics stop updating.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {sync ? (
        <SyncMonitor sync={sync} />
      ) : (
        <p className='text-muted-foreground text-sm'>
          {t('The sync has never run for this pool.')}
        </p>
      )}

      <form
        onSubmit={(event) => void save(event)}
        className='space-y-3 rounded-lg border p-3'
      >
        <div className='flex items-center justify-between gap-4'>
          <div className='space-y-0.5'>
            <Label htmlFor={`pool-sync-enabled-${pool.id}`}>
              {t('Sync logs to MongoDB')}
            </Label>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Reads the pool’s logs through its API, one request per window. Synced logs are kept for 30 days.'
              )}
            </p>
          </div>
          <Switch
            id={`pool-sync-enabled-${pool.id}`}
            checked={enabled}
            onCheckedChange={setEnabled}
          />
        </div>
        <div className='grid gap-3 sm:grid-cols-2'>
          {FIELDS.map((field) => {
            const id = `pool-sync-${field.key}-${pool.id}`
            const bad = invalid.includes(field.key)
            return (
              <div key={field.key} className='space-y-1'>
                <Label htmlFor={id}>{t(field.label)}</Label>
                <Input
                  id={id}
                  type='number'
                  inputMode='numeric'
                  min={field.min}
                  max={field.max}
                  step={1}
                  aria-invalid={bad}
                  aria-describedby={`${id}-hint`}
                  value={values[field.key]}
                  onChange={(event) =>
                    setValues((current) => ({
                      ...current,
                      [field.key]: event.target.value,
                    }))
                  }
                />
                <p
                  id={`${id}-hint`}
                  className={cn(
                    'text-xs',
                    bad ? 'text-destructive' : 'text-muted-foreground'
                  )}
                >
                  {t('{{min}} to {{max}}', { min: field.min, max: field.max })}
                  {field.key === 'backfill_days' &&
                    ` · ${t('Used when the sync first starts and on clear and resync')}`}
                </p>
              </div>
            )
          })}
        </div>
        <Button
          type='submit'
          size='sm'
          disabled={invalid.length > 0 || busy !== null}
        >
          {busy === 'save' && <Loader2 className='size-4 animate-spin' />}
          {t('Save changes')}
        </Button>
      </form>

      <div className='flex flex-wrap gap-2'>
        <Button
          variant='outline'
          size='sm'
          onClick={() => setConflictsOpen(true)}
        >
          {t('Conflicts')}
          {sync && sync.open_conflicts > 0 && ` (${sync.open_conflicts})`}
        </Button>
        {pool.db.set && (
          <Button
            variant='outline'
            size='sm'
            disabled={busy !== null || !sync}
            onClick={() => void verify()}
          >
            {busy === 'verify' && <Loader2 className='size-4 animate-spin' />}
            {t('Verify the last 24 hours')}
          </Button>
        )}
        <Button
          variant='outline'
          size='sm'
          className='text-destructive hover:text-destructive'
          disabled={busy !== null || !sync || invalid.includes('backfill_days')}
          onClick={() => setConfirmReset(true)}
        >
          {busy === 'reset' && <Loader2 className='size-4 animate-spin' />}
          {t('Clear and resync')}
        </Button>
      </div>
      {verified && <VerifyResult result={verified} />}

      <ConfirmDialog
        open={confirmReset}
        onOpenChange={setConfirmReset}
        title={t('Clear and resync')}
        desc={t(
          'Delete every log synced from "{{name}}" and sync again from {{days}} days back? Open conflicts are marked resolved. Until the sync catches up, its logs and statistics are incomplete.',
          { name: pool.name, days: numbers.backfill_days }
        )}
        confirmText={t('Clear and resync')}
        destructive
        handleConfirm={() => void reset()}
      />
      <PoolConflictsDialog
        pool={pool}
        open={conflictsOpen}
        onOpenChange={setConflictsOpen}
      />
      {proofDialog}
    </div>
  )
}
