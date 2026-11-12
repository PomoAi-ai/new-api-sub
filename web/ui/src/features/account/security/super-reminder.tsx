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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { MFA_REMINDER_KEY } from '@/lib/auth-session'
import { useAuthStore } from '@/stores/auth-store'

import { AccountSecurityDialog } from './account-security-dialog'

function readDismissed(): boolean {
  try {
    return sessionStorage.getItem(MFA_REMINDER_KEY) === '1'
  } catch {
    return false
  }
}

function writeDismissed(): void {
  try {
    sessionStorage.setItem(MFA_REMINDER_KEY, '1')
  } catch {
    // Storage is blocked: the dismissal lasts until the page reloads.
  }
}

/** Nudges a super without a verification factor; never blocks anything. */
export function SuperMfaReminder() {
  const { t } = useTranslation()
  const needed = useAuthStore((s) => {
    const user = s.auth.user
    return user?.role === 'super' && !user.mfa_enabled
  })
  const [dismissed, setDismissed] = useState(readDismissed)
  const [settingUp, setSettingUp] = useState(false)

  return (
    <>
      <ConfirmDialog
        open={needed && !dismissed && !settingUp}
        onOpenChange={(open) => {
          if (open) return
          writeDismissed()
          setDismissed(true)
        }}
        title={t('Protect your super admin account')}
        desc={t(
          'This account has no two-step verification. Add an authenticator app or a passkey so a stolen password alone cannot sign in.'
        )}
        cancelBtnText={t('Ignore')}
        confirmText={t('Set up now')}
        handleConfirm={() => setSettingUp(true)}
      />
      <AccountSecurityDialog open={settingUp} onOpenChange={setSettingUp} />
    </>
  )
}
