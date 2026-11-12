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
import {
  Check,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
  TriangleAlert,
  X,
} from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { PasswordInput } from '@/components/password-input'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { ProofCancelled } from '@/features/account/security/api'
import { useProof } from '@/features/account/security/use-proof'
import { handleServerError } from '@/lib/handle-server-error'

import {
  createPool,
  deletePool,
  getPools,
  poolsQueryKey,
  testPool,
  updatePool,
  type Pool,
  type PoolInput,
  type PoolLogMode,
  type PoolTestResult,
} from '../api'
import { PoolSyncPanel } from './pool-sync-panel'

const emptySecrets = { pat: '', db_dsn: '', log_dsn: '', key_dsn: '' }
type SecretField = keyof typeof emptySecrets

const RESTART_NOTICE =
  'Pool saved. The services restart within about 10 seconds; refresh this page afterwards.'

function normalizeAddress(address: string): string {
  return address.trim().replace(/\/+$/, '')
}

function SecretInput(props: {
  id: SecretField
  label: string
  required?: boolean
  saved: boolean
  hint?: string
  value: string
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  return (
    <div className='space-y-2'>
      <Label htmlFor={`pool-${props.id}`}>
        {props.label}
        {props.required && <span className='text-destructive'>*</span>}
      </Label>
      <PasswordInput
        id={`pool-${props.id}`}
        autoComplete='new-password'
        spellCheck={false}
        className='font-mono'
        placeholder={props.saved ? t('Saved. Leave empty to keep it.') : ''}
        value={props.value}
        onChange={(event) => props.onChange(event.target.value)}
      />
      {props.hint && (
        <p className='text-muted-foreground font-mono text-xs'>{props.hint}</p>
      )}
    </div>
  )
}

function ClearCheckbox(props: {
  id: string
  label: string
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <div className='flex items-center gap-2'>
      <Checkbox
        id={props.id}
        checked={props.checked}
        onCheckedChange={(checked) => props.onChange(checked === true)}
      />
      <Label htmlFor={props.id} className='font-normal'>
        {props.label}
      </Label>
    </div>
  )
}

function TestResult({ result }: { result: PoolTestResult }) {
  return (
    <ul className='space-y-1 rounded-lg border p-3 text-sm'>
      {result.steps.map((step) => (
        <li key={step.name} className='flex items-start gap-2'>
          {step.ok ? (
            <Check className='text-success mt-0.5 size-4 shrink-0' />
          ) : (
            <X className='text-destructive mt-0.5 size-4 shrink-0' />
          )}
          <span>
            {step.name}
            {step.message && (
              <span className='text-muted-foreground'> {step.message}</span>
            )}
          </span>
        </li>
      ))}
    </ul>
  )
}

/**
 * pool null adds one. Secrets stay in this form's state and are dropped once
 * saved or closed.
 */
function PoolForm(props: { pool: Pool | null; onSaved: () => void }) {
  const { pool } = props
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { ask, dialog: proofDialog } = useProof()
  const [name, setName] = useState(pool?.name ?? '')
  const [baseUrl, setBaseUrl] = useState(pool?.base_url ?? '')
  const [logMode, setLogMode] = useState<PoolLogMode>(
    pool?.log_mode ?? 'direct'
  )
  const [acceptNew, setAcceptNew] = useState(pool?.accept_new ?? true)
  const [secrets, setSecrets] = useState(emptySecrets)
  const [clearLog, setClearLog] = useState(false)
  const [clearKey, setClearKey] = useState(false)
  const [addressConfirmed, setAddressConfirmed] = useState(false)
  const [busy, setBusy] = useState<'test' | 'save' | null>(null)
  const [result, setResult] = useState<PoolTestResult | null>(null)

  const addressChanged =
    pool !== null &&
    normalizeAddress(baseUrl) !== normalizeAddress(pool.base_url)
  const patRequired = !pool?.pat_set || addressChanged
  const dbRequired = logMode === 'direct' && !pool?.db.set
  const trimmedName = name.trim()
  const ready =
    trimmedName.length >= 1 &&
    trimmedName.length <= 40 &&
    normalizeAddress(baseUrl) !== '' &&
    (secrets.pat !== '' || !patRequired) &&
    (secrets.db_dsn !== '' || !dbRequired) &&
    (!addressChanged || addressConfirmed)

  const setSecret = (field: SecretField) => (value: string) =>
    setSecrets((current) => ({ ...current, [field]: value }))

  const logModeItems = [
    { value: 'direct', label: t('Read the new-api database') },
    { value: 'sync', label: t('Sync to MongoDB') },
  ]

  const input = (): PoolInput => ({
    ...secrets,
    name: trimmedName,
    base_url: normalizeAddress(baseUrl),
    log_mode: logMode,
    accept_new: acceptNew,
    clear_log_dsn: clearLog,
    clear_key_dsn: clearKey,
    // A new pool has no old address to be confused with.
    confirm_address_change: pool === null || addressConfirmed,
  })

  const test = async () => {
    setBusy('test')
    setResult(null)
    try {
      setResult(await testPool(pool?.id ?? 0, input()))
    } catch (error) {
      handleServerError(error, t('Failed to test the connection'))
    } finally {
      setBusy(null)
    }
  }

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setBusy('save')
    try {
      const proof = await ask('newapi_config')
      if (pool) {
        await updatePool(pool.id, input(), proof)
      } else {
        await createPool(input(), proof)
      }
      toast.success(t(RESTART_NOTICE))
      void queryClient.invalidateQueries({ queryKey: poolsQueryKey })
      props.onSaved()
    } catch (error) {
      if (!(error instanceof ProofCancelled)) {
        handleServerError(error, t('Failed to save the pool'))
      }
    } finally {
      setBusy(null)
    }
  }

  return (
    <form onSubmit={(event) => void save(event)} className='space-y-4'>
      <div className='space-y-2'>
        <Label htmlFor='pool-name'>
          {t('Pool Name')}
          <span className='text-destructive'>*</span>
        </Label>
        <Input
          id='pool-name'
          autoComplete='off'
          maxLength={40}
          value={name}
          onChange={(event) => setName(event.target.value)}
        />
      </div>
      <div className='space-y-2'>
        <Label htmlFor='pool-base-url'>
          {t('Address')}
          <span className='text-destructive'>*</span>
        </Label>
        <Input
          id='pool-base-url'
          className='font-mono'
          autoComplete='off'
          spellCheck={false}
          value={baseUrl}
          onChange={(event) => setBaseUrl(event.target.value)}
        />
      </div>
      <SecretInput
        id='pat'
        label={t('Access Token')}
        required={patRequired}
        saved={Boolean(pool?.pat_set) && !addressChanged}
        value={secrets.pat}
        onChange={setSecret('pat')}
      />
      {addressChanged && (
        <Alert variant='destructive'>
          <TriangleAlert />
          <AlertTitle>{t('The address is changing')}</AlertTitle>
          <AlertDescription>
            <p>
              {t(
                'Enter the access token again: the saved one is never sent to a new address. If this is a different new-api instance, every existing channel id will point at the wrong channel.'
              )}
            </p>
            <div className='mt-2'>
              <ClearCheckbox
                id='pool-confirm-address'
                label={t('I understand and want to change the address')}
                checked={addressConfirmed}
                onChange={setAddressConfirmed}
              />
            </div>
          </AlertDescription>
        </Alert>
      )}
      <div className='space-y-2'>
        <Label htmlFor='pool-log-mode'>{t('Log Source')}</Label>
        <Select
          items={logModeItems}
          value={logMode}
          onValueChange={(value) => {
            if (value === 'direct' || value === 'sync') setLogMode(value)
          }}
        >
          <SelectTrigger id='pool-log-mode' className='w-full'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              {logModeItems.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        {logMode === 'sync' && (
          <p className='text-muted-foreground text-xs'>
            {t(
              'Logs come from MongoDB, pulled through the pool’s API, and only the last 30 days can be read. The main database is optional. Turn the sync on after saving.'
            )}
          </p>
        )}
      </div>
      <SecretInput
        id='db_dsn'
        label={t('Main Database')}
        required={dbRequired}
        saved={Boolean(pool?.db.set)}
        hint={pool?.db.host}
        value={secrets.db_dsn}
        onChange={setSecret('db_dsn')}
      />
      <SecretInput
        id='log_dsn'
        label={t('Log Database')}
        saved={Boolean(pool?.log_db.set)}
        hint={pool?.log_db.host || t('Empty: same as the main database')}
        value={secrets.log_dsn}
        onChange={setSecret('log_dsn')}
      />
      {pool?.log_db.set && (
        <ClearCheckbox
          id='pool-clear-log'
          label={t('Clear the log database connection')}
          checked={clearLog}
          onChange={setClearLog}
        />
      )}
      <SecretInput
        id='key_dsn'
        label={t('Key Database')}
        saved={Boolean(pool?.key_db.set)}
        hint={
          pool?.key_db.host ||
          t('Empty: per-key test and balance are unavailable')
        }
        value={secrets.key_dsn}
        onChange={setSecret('key_dsn')}
      />
      {pool?.key_db.set && (
        <ClearCheckbox
          id='pool-clear-key'
          label={t('Clear the key database connection')}
          checked={clearKey}
          onChange={setClearKey}
        />
      )}
      <div className='flex items-center justify-between gap-4 rounded-lg border p-3'>
        <div className='space-y-0.5'>
          <Label htmlFor='pool-accept-new'>{t('Accept new channels')}</Label>
          <p className='text-muted-foreground text-xs'>
            {t(
              'When off, new channels skip this pool. Existing channels and logs are not affected.'
            )}
          </p>
        </div>
        <Switch
          id='pool-accept-new'
          checked={acceptNew}
          onCheckedChange={setAcceptNew}
        />
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Test first: saving runs the same checks and must finish within 30 seconds. Saved values cannot be read back. Saving restarts the portal and this console.'
        )}
      </p>
      {result && <TestResult result={result} />}
      <div className='flex flex-wrap gap-2'>
        <Button
          type='button'
          variant='outline'
          disabled={!ready || busy !== null}
          onClick={() => void test()}
        >
          {busy === 'test' && <Loader2 className='size-4 animate-spin' />}
          {t('Test Connection')}
        </Button>
        <Button type='submit' disabled={!ready || busy !== null}>
          {busy === 'save' && <Loader2 className='size-4 animate-spin' />}
          {pool ? t('Save changes') : t('Add Pool')}
        </Button>
      </div>
      {proofDialog}
    </form>
  )
}

function PoolRow(props: {
  pool: Pool
  onEdit: () => void
  onSync: () => void
  onDelete: () => void
}) {
  const { pool } = props
  const { t } = useTranslation()
  const inUse = pool.placements > 0 || pool.templates > 0
  return (
    <li className='flex flex-wrap items-start justify-between gap-3 p-3'>
      <div className='min-w-0 space-y-1'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='font-medium'>{pool.name}</span>
          <span className='text-muted-foreground font-mono text-xs'>
            #{pool.id}
          </span>
          {pool.available ? (
            <StatusBadge
              label={t('Available')}
              variant='success'
              size='sm'
              copyable={false}
            />
          ) : (
            <StatusBadge
              label={t('Pool unavailable')}
              variant='danger'
              size='sm'
              copyable={false}
            />
          )}
          {!pool.accept_new && (
            <StatusBadge
              label={t('Not accepting new channels')}
              variant='warning'
              size='sm'
              copyable={false}
            />
          )}
          {pool.sync?.paused && (
            <StatusBadge
              label={t('Sync paused')}
              variant='danger'
              size='sm'
              copyable={false}
            />
          )}
          {pool.log_mode === 'sync' && !pool.sync?.enabled && (
            <StatusBadge
              label={t('Sync off')}
              variant='warning'
              size='sm'
              copyable={false}
            />
          )}
        </div>
        <p className='text-muted-foreground font-mono text-xs break-all'>
          {pool.base_url}
        </p>
        {!pool.available && pool.reason && (
          <p className='text-destructive text-xs'>{pool.reason}</p>
        )}
        <p className='text-muted-foreground text-xs'>
          {pool.log_mode === 'sync'
            ? t('Sync to MongoDB')
            : t('Read the new-api database')}
          {' · '}
          {t('{{channels}} channels, {{templates}} templates', {
            channels: pool.placements,
            templates: pool.templates,
          })}
        </p>
      </div>
      <div className='flex shrink-0 gap-1'>
        <Button
          variant='ghost'
          size='icon-sm'
          aria-label={t('Log sync')}
          title={t('Log sync')}
          onClick={props.onSync}
        >
          <RefreshCw className='size-4' />
        </Button>
        <Button
          variant='ghost'
          size='icon-sm'
          aria-label={t('Edit')}
          onClick={props.onEdit}
        >
          <Pencil className='size-4' />
        </Button>
        <Button
          variant='ghost'
          size='icon-sm'
          className='text-destructive hover:text-destructive'
          aria-label={
            inUse
              ? t('A pool with channels or templates cannot be deleted')
              : t('Delete')
          }
          disabled={inUse}
          onClick={props.onDelete}
        >
          <Trash2 className='size-4' />
        </Button>
      </div>
    </li>
  )
}

export function PoolsCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { ask, dialog: proofDialog } = useProof()
  // 'new' adds a pool; null is closed.
  const [editing, setEditing] = useState<Pool | 'new' | null>(null)
  const [deleting, setDeleting] = useState<Pool | null>(null)
  // By id, so the panel shows the pool as the list refreshes.
  const [syncing, setSyncing] = useState<number | null>(null)
  const query = useQuery({
    queryKey: poolsQueryKey,
    queryFn: getPools,
    meta: { errorToast: false, errorRedirect: false },
    refetchInterval: syncing === null ? false : 15_000,
  })

  const remove = async (pool: Pool) => {
    setDeleting(null)
    try {
      await deletePool(pool.id, await ask('newapi_config'))
      toast.success(t(RESTART_NOTICE))
      void queryClient.invalidateQueries({ queryKey: poolsQueryKey })
    } catch (error) {
      if (!(error instanceof ProofCancelled)) {
        handleServerError(error, t('Failed to delete the pool'))
      }
    }
  }

  if (query.isError) {
    return (
      <ErrorState
        title={t('Failed to load the pools')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  if (!query.data) return <LoadingState message={t('Loading...')} />

  const editingPool = editing === 'new' ? null : editing
  const syncPool = query.data.find((pool) => pool.id === syncing)
  return (
    <div className='space-y-3'>
      {query.data.length === 0 ? (
        <Alert variant='destructive'>
          <TriangleAlert />
          <AlertTitle>{t('Restricted mode')}</AlertTitle>
          <AlertDescription>
            {t('No pool is configured yet. Add one to manage channels.')}
          </AlertDescription>
        </Alert>
      ) : (
        <ul className='divide-y rounded-lg border'>
          {query.data.map((pool) => (
            <PoolRow
              key={pool.id}
              pool={pool}
              onEdit={() => setEditing(pool)}
              onSync={() => setSyncing(pool.id)}
              onDelete={() => setDeleting(pool)}
            />
          ))}
        </ul>
      )}
      <Button variant='outline' size='sm' onClick={() => setEditing('new')}>
        <Plus className='size-4' />
        {t('Add Pool')}
      </Button>
      <Dialog
        open={editing !== null}
        onOpenChange={(open) => !open && setEditing(null)}
        title={editingPool ? t('Edit Pool') : t('Add Pool')}
      >
        {editing !== null && (
          <PoolForm
            key={editingPool ? `${editingPool.id}-${editingPool.version}` : 0}
            pool={editingPool}
            onSaved={() => setEditing(null)}
          />
        )}
      </Dialog>
      <Dialog
        open={syncPool !== undefined}
        onOpenChange={(open) => !open && setSyncing(null)}
        title={t('Log sync of {{name}}', { name: syncPool?.name ?? '' })}
      >
        {syncPool && <PoolSyncPanel key={syncPool.id} pool={syncPool} />}
      </Dialog>
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('Delete Pool')}
        desc={t(
          'Delete pool "{{name}}"? Its synced logs expire on their own. Deleting restarts the portal and this console.',
          { name: deleting?.name ?? '' }
        )}
        confirmText={t('Delete')}
        destructive
        handleConfirm={() => {
          if (deleting) void remove(deleting)
        }}
      />
      {proofDialog}
    </div>
  )
}
