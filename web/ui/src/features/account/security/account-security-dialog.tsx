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
import { Fingerprint, KeySquare, Loader2, Smartphone } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { usePasskeySupported } from '@/features/auth/lib/use-passkey-supported'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  ProofCancelled,
  bindPasskey,
  confirmTotp,
  getSecurityStatus,
  regenerateBackupCodes,
  securityQueryKey,
  startTotpSetup,
  unbindPasskey,
  unbindTotp,
  type TotpSetup,
} from './api'
import { CodeInput } from './factor-form'
import { useProof } from './use-proof'

function FactorRow(props: {
  icon: ReactNode
  title: string
  description: string
  status?: ReactNode
  children: ReactNode
}) {
  return (
    <div className='flex flex-wrap items-center gap-3 rounded-lg border p-3'>
      <div className='text-muted-foreground'>{props.icon}</div>
      <div className='min-w-0 flex-1 basis-40'>
        <div className='flex items-center gap-2 text-sm font-medium'>
          {props.title}
          {props.status}
        </div>
        <p className='text-muted-foreground text-xs'>{props.description}</p>
      </div>
      <div className='flex gap-2'>{props.children}</div>
    </div>
  )
}

function BoundBadge({ bound }: { bound: boolean }) {
  const { t } = useTranslation()
  return (
    <StatusBadge
      label={bound ? t('Enabled') : t('Not set up')}
      variant={bound ? 'success' : 'neutral'}
      copyable={false}
      className='font-normal'
    />
  )
}

function TotpSetupForm(props: {
  setup: TotpSetup
  busy: boolean
  onConfirm: (code: string) => Promise<void>
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const [code, setCode] = useState('')
  const confirm = (value: string) => {
    if (props.busy) return
    void props.onConfirm(value).finally(() => setCode(''))
  }

  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Scan the QR code with an authenticator app, or enter the key by hand. Then type the code it shows to finish.'
        )}
      </p>
      <div className='mx-auto w-fit rounded-lg bg-white p-3'>
        <QRCodeSVG value={props.setup.otpauth_url} size={176} />
      </div>
      <div className='flex items-center gap-2 rounded-lg border p-2'>
        <code className='min-w-0 flex-1 font-mono text-xs break-all'>
          {props.setup.secret}
        </code>
        <CopyButton value={props.setup.secret} className='size-7' />
      </div>
      <CodeInput
        value={code}
        disabled={props.busy}
        onChange={setCode}
        onComplete={confirm}
      />
      <div className='flex justify-end gap-2'>
        <Button
          type='button'
          variant='outline'
          disabled={props.busy}
          onClick={props.onCancel}
        >
          {t('Cancel')}
        </Button>
        <Button
          type='button'
          disabled={props.busy || code.length < 6}
          onClick={() => confirm(code)}
        >
          {props.busy && <Loader2 className='size-4 animate-spin' />}
          {t('Confirm')}
        </Button>
      </div>
    </div>
  )
}

function BackupCodesDialog(props: {
  codes: string[] | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  return (
    <Dialog
      open={props.codes !== null}
      // The only place the codes can be seen: Escape, a click outside and the
      // corner button all do nothing, only the button below closes it.
      onOpenChange={() => {}}
      showCloseButton={false}
      title={t('Backup codes')}
      description={t(
        'Save these codes now. Once this dialog is closed they cannot be viewed again. Each code works once.'
      )}
      contentClassName='sm:max-w-md'
      footer={
        <Button type='button' onClick={props.onClose}>
          {t('I have saved them')}
        </Button>
      }
    >
      <div className='flex items-start gap-2 rounded-lg border p-3'>
        <pre className='grid min-w-0 flex-1 grid-cols-1 gap-1 font-mono text-sm select-all sm:grid-cols-2'>
          {props.codes?.map((code) => (
            <span key={code}>{code}</span>
          ))}
        </pre>
        <CopyButton value={props.codes?.join('\n') ?? ''} className='size-7' />
      </div>
    </Dialog>
  )
}

/** Binds and removes the signed-in account's verification factors. */
export function AccountSecurityDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const passkeySupported = usePasskeySupported()
  const { ask, dialog: proofDialog } = useProof()
  const [busy, setBusy] = useState(false)
  const [setup, setSetup] = useState<TotpSetup | null>(null)
  const [codes, setCodes] = useState<string[] | null>(null)

  const query = useQuery({
    queryKey: securityQueryKey,
    queryFn: getSecurityStatus,
    enabled: props.open,
    staleTime: 0,
    meta: { errorToast: false, errorRedirect: false },
  })
  const status = query.data
  const mfaEnabled = status ? status.totp || status.passkey : undefined

  // The super reminder reads this from the store.
  useEffect(() => {
    if (mfaEnabled === undefined) return
    const { user, setUser } = useAuthStore.getState().auth
    if (user && user.mfa_enabled !== mfaEnabled) {
      setUser({ ...user, mfa_enabled: mfaEnabled })
    }
  }, [mfaEnabled])

  const run = async (action: () => Promise<void>, failure: string) => {
    setBusy(true)
    try {
      await action()
      void queryClient.invalidateQueries({ queryKey: securityQueryKey })
    } catch (error) {
      if (!(error instanceof ProofCancelled)) {
        handleServerError(error, failure)
      }
    } finally {
      setBusy(false)
    }
  }

  const startTotp = () =>
    run(async () => {
      setSetup(await startTotpSetup(await ask('totp_bind')))
    }, t('Failed to start the setup'))

  const finishTotp = (code: string) =>
    run(async () => {
      if (!setup) return
      const backupCodes = await confirmTotp(setup.flow_token, code)
      setSetup(null)
      toast.success(t('Authenticator app added'))
      if (backupCodes) setCodes(backupCodes)
    }, t('Verification failed'))

  const removeTotp = () =>
    run(async () => {
      await unbindTotp(await ask('totp_unbind'))
      toast.success(t('Authenticator app removed'))
    }, t('Failed to remove the authenticator app'))

  const addPasskey = () =>
    run(async () => {
      const backupCodes = await bindPasskey(await ask('passkey_bind'))
      toast.success(t('Passkey added'))
      if (backupCodes) setCodes(backupCodes)
    }, t('Failed to add the passkey'))

  const removePasskey = () =>
    run(async () => {
      await unbindPasskey(await ask('passkey_unbind'))
      toast.success(t('Passkey removed'))
    }, t('Failed to remove the passkey'))

  const regenerate = () =>
    run(async () => {
      setCodes(await regenerateBackupCodes(await ask('backup_regenerate')))
    }, t('Failed to generate backup codes'))

  let content = <LoadingState message={t('Loading...')} />
  if (setup) {
    content = (
      <TotpSetupForm
        setup={setup}
        busy={busy}
        onConfirm={finishTotp}
        onCancel={() => setSetup(null)}
      />
    )
  } else if (status) {
    content = (
      <div className='space-y-3'>
        <FactorRow
          icon={<Smartphone className='size-5' />}
          title={t('Authenticator app')}
          description={t(
            'A 6-digit code at sign-in, from an app on your phone.'
          )}
          status={<BoundBadge bound={status.totp} />}
        >
          {status.totp ? (
            <Button
              variant='outline'
              size='sm'
              disabled={busy}
              onClick={() => void removeTotp()}
            >
              {t('Remove')}
            </Button>
          ) : (
            <Button size='sm' disabled={busy} onClick={() => void startTotp()}>
              {t('Set up')}
            </Button>
          )}
        </FactorRow>
        <FactorRow
          icon={<Fingerprint className='size-5' />}
          title={t('Passkey')}
          description={t(
            'Sign in with your fingerprint, face or security key, without a password.'
          )}
          status={<BoundBadge bound={status.passkey} />}
        >
          {status.passkey ? (
            <Button
              variant='outline'
              size='sm'
              disabled={busy}
              onClick={() => void removePasskey()}
            >
              {t('Remove')}
            </Button>
          ) : (
            <Button
              size='sm'
              disabled={busy || !passkeySupported}
              onClick={() => void addPasskey()}
            >
              {t('Set up')}
            </Button>
          )}
        </FactorRow>
        {mfaEnabled && (
          <FactorRow
            icon={<KeySquare className='size-5' />}
            title={t('Backup codes')}
            description={t('{{count}} left', {
              count: status.backup_codes_left,
            })}
          >
            <Button
              variant='outline'
              size='sm'
              disabled={busy}
              onClick={() => void regenerate()}
            >
              {t('Generate new codes')}
            </Button>
          </FactorRow>
        )}
        <p className='text-muted-foreground text-xs'>
          {t(
            'Any change here signs out your other sessions. API tokens keep working.'
          )}
        </p>
      </div>
    )
  } else if (query.isError) {
    content = (
      <ErrorState
        title={t('Failed to load account security')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        // Unsaved backup codes and running steps must not vanish with the dialog.
        if (!open && (busy || codes)) return
        if (!open) setSetup(null)
        props.onOpenChange(open)
      }}
      title={t('Account Security')}
      contentClassName='sm:max-w-lg'
    >
      {content}
      {proofDialog}
      <BackupCodesDialog codes={codes} onClose={() => setCodes(null)} />
    </Dialog>
  )
}
