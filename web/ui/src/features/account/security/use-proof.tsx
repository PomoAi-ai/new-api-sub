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
import { useCallback, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'

import {
  ProofCancelled,
  getSecurityStatus,
  requestPasskeyProof,
  requestProof,
  securityQueryKey,
  type ProofPurpose,
} from './api'
import { FactorForm } from './factor-form'

const PURPOSE_TEXT: Record<ProofPurpose, string> = {
  totp_bind: 'Confirm it is you before adding an authenticator app.',
  totp_unbind:
    'Confirm it is you before removing the authenticator app. If it is your last verification method, your backup codes are deleted too.',
  passkey_bind: 'Confirm it is you before adding a passkey.',
  passkey_unbind:
    'Confirm it is you before removing the passkey. If it is your last verification method, your backup codes are deleted too.',
  backup_regenerate:
    'Confirm it is you before generating new backup codes. Your current codes stop working.',
  token_create: 'Confirm it is you before creating an API token.',
  newapi_config: 'Confirm it is you before saving the connection.',
}

// oxlint-disable-next-line react/only-export-components -- only useProof renders it
function ProofDialog(props: {
  purpose: ProofPurpose
  onProof: (proof: string) => void
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: securityQueryKey,
    queryFn: getSecurityStatus,
    staleTime: 0,
    meta: { errorToast: false, errorRedirect: false },
  })

  let content = <LoadingState message={t('Loading...')} />
  if (query.data) {
    content = (
      <FactorForm
        methods={query.data.methods}
        submitText={t('Verify')}
        onAnswer={async (answer) => {
          props.onProof(await requestProof(props.purpose, answer))
        }}
        onPasskey={async () => {
          props.onProof(await requestPasskeyProof(props.purpose))
        }}
      />
    )
  } else if (query.isError) {
    content = (
      <ErrorState
        title={t('Failed to load verification methods')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onCancel()
      }}
      title={t('Verify your identity')}
      description={t(PURPOSE_TEXT[props.purpose])}
      contentClassName='sm:max-w-md'
    >
      {content}
    </Dialog>
  )
}

/**
 * `ask` opens the prompt and resolves with a one-time proof. Render `dialog`
 * inside the dialog that calls it, if any, so the two stack properly.
 */
export function useProof() {
  const [purpose, setPurpose] = useState<ProofPurpose | null>(null)
  const pending = useRef<{
    resolve: (proof: string) => void
    reject: (reason: ProofCancelled) => void
  } | null>(null)

  const ask = useCallback(
    (next: ProofPurpose) =>
      new Promise<string>((resolve, reject) => {
        pending.current?.reject(new ProofCancelled())
        pending.current = { resolve, reject }
        setPurpose(next)
      }),
    []
  )
  const settle = (
    finish: (current: NonNullable<typeof pending.current>) => void
  ) => {
    if (pending.current) finish(pending.current)
    pending.current = null
    setPurpose(null)
  }

  const dialog = purpose && (
    <ProofDialog
      purpose={purpose}
      onProof={(proof) => settle((current) => current.resolve(proof))}
      onCancel={() => settle((current) => current.reject(new ProofCancelled()))}
    />
  )
  return { ask, dialog }
}
