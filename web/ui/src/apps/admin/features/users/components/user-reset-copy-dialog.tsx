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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { resetUserPassword, type PortalUser } from '../api'
import { randomPassword } from '../lib'

type UserResetCopyDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: PortalUser | null
}

export function UserResetCopyDialog(props: UserResetCopyDialogProps) {
  const { t } = useTranslation()
  const portalUrl = useAuthStore((s) => s.auth.user?.portal_url)
  const { copyToClipboard } = useCopyToClipboard()
  const [pending, setPending] = useState(false)
  // Only set when the clipboard refused: the new password is shown nowhere else.
  const [uncopied, setUncopied] = useState<string | null>(null)

  useEffect(() => {
    if (!props.open) setUncopied(null)
  }, [props.open])

  const handleConfirm = async () => {
    if (!props.user) return
    const password = randomPassword()
    setPending(true)
    try {
      await resetUserPassword(props.user.id, password)
      const text = [
        `${t('Login URL')}: ${portalUrl}`,
        `${t('Username')}: ${props.user.username}`,
        `${t('Password')}: ${password}`,
      ].join('\n')
      if (await copyToClipboard(text)) {
        props.onOpenChange(false)
      } else {
        setUncopied(text)
      }
    } catch (error) {
      handleServerError(error, t('Failed to reset password'))
    } finally {
      setPending(false)
    }
  }

  if (uncopied) {
    return (
      <Dialog
        open={props.open}
        // The only place the new password can be seen: Escape, a click outside
        // and the corner button all do nothing, only the button below closes it.
        onOpenChange={() => {}}
        showCloseButton={false}
        title={t('Password reset, but it could not be copied')}
        description={t(
          'The password is shown only once. Copy it and send it to the user.'
        )}
        contentClassName='sm:max-w-lg'
        contentHeight='auto'
        footer={
          <Button type='button' onClick={() => props.onOpenChange(false)}>
            {t('I have saved it')}
          </Button>
        }
      >
        <div className='flex items-start gap-2 rounded-lg border p-3'>
          <pre className='min-w-0 flex-1 font-mono text-sm break-all whitespace-pre-wrap select-all'>
            {uncopied}
          </pre>
          <CopyButton value={uncopied} className='size-7' />
        </div>
      </Dialog>
    )
  }

  return (
    <ConfirmDialog
      open={props.open}
      // The reset may already have happened: wait to see the outcome.
      onOpenChange={(open) => {
        if (!pending) props.onOpenChange(open)
      }}
      title={t('Reset and Copy')}
      desc={t(
        'A new random password replaces the current one of "{{name}}" and the user is signed out everywhere. The login details are then copied.',
        { name: props.user?.username ?? '' }
      )}
      confirmText={t('Reset and Copy')}
      destructive
      isLoading={pending}
      disabled={pending}
      handleConfirm={() => void handleConfirm()}
    />
  )
}
