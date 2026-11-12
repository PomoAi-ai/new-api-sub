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
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { FactorForm } from '@/features/account/security/factor-form'
import { loginSecondStep, signInWithPasskey } from '@/features/auth/api'
import type { LoginPending } from '@/features/auth/types'

/** The flow token lives only in the parent's state, for this one attempt. */
export function SecondStepForm(props: {
  pending: LoginPending
  onBack: () => void
  onSuccess: () => Promise<void>
}) {
  const { t } = useTranslation()
  const { pending, onBack, onSuccess } = props

  // Once expired every code fails with the same message as a wrong one.
  useEffect(() => {
    const timer = setTimeout(() => {
      toast.error(t('The sign-in attempt expired. Enter your password again.'))
      onBack()
    }, pending.expires_in * 1000)
    return () => clearTimeout(timer)
  }, [pending, onBack, t])

  return (
    <div className='grid gap-3'>
      <FactorForm
        methods={pending.methods}
        submitText={t('Verify')}
        onAnswer={async (answer) => {
          if ('password' in answer) return
          await loginSecondStep({ flow_token: pending.flow_token, ...answer })
          await onSuccess()
        }}
        onPasskey={async () => {
          await signInWithPasskey()
          await onSuccess()
        }}
      />
      <Button type='button' variant='ghost' size='sm' onClick={onBack}>
        {t('Back to sign in')}
      </Button>
    </div>
  )
}
