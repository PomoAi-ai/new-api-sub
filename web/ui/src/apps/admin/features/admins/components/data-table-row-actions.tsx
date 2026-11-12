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
// Modified 2026-11-06 by new-api-sub: admin actions: reset password, reset two-step verification, enable/disable, delete; own row cannot be disabled, deleted or reset (from users/components/data-table-row-actions.tsx)
import { useQueryClient } from '@tanstack/react-query'
import type { Row } from '@tanstack/react-table'
import { KeyRound, Power, PowerOff, ShieldOff, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTableRowActionMenu } from '@/components/data-table/core/row-action-menu'
import { Button } from '@/components/ui/button'
import {
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
} from '@/components/ui/dropdown-menu'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  ADMIN_STATUS,
  adminsQueryKey,
  resetAdminMfa,
  setAdminStatus,
  type AdminAccount,
} from '../api'
import { useAdmins } from './admins-provider'

interface DataTableRowActionsProps {
  row: Row<AdminAccount>
}

export function DataTableRowActions({ row }: DataTableRowActionsProps) {
  const { t } = useTranslation()
  const admin = row.original
  const queryClient = useQueryClient()
  const { setOpen, setCurrentRow } = useAdmins()
  const isSelf = useAuthStore((s) => s.auth.user?.id === admin.id)
  const isDisabled = admin.status === ADMIN_STATUS.DISABLED
  const [resettingMfa, setResettingMfa] = useState(false)
  const [mfaBusy, setMfaBusy] = useState(false)

  const openDialog = (dialog: 'password' | 'delete') => {
    setCurrentRow(admin)
    setOpen(dialog)
  }

  const handleResetMfa = async () => {
    setMfaBusy(true)
    try {
      await resetAdminMfa(admin.id)
      toast.success(t('Two-step verification reset'))
      setResettingMfa(false)
      void queryClient.invalidateQueries({ queryKey: adminsQueryKey })
    } catch (error) {
      handleServerError(error, t('Failed to reset two-step verification'))
    } finally {
      setMfaBusy(false)
    }
  }

  const handleToggleStatus = async () => {
    try {
      await setAdminStatus(
        admin.id,
        isDisabled ? ADMIN_STATUS.ENABLED : ADMIN_STATUS.DISABLED
      )
      toast.success(
        isDisabled
          ? t('Admin enabled successfully')
          : t('Admin disabled successfully')
      )
      void queryClient.invalidateQueries({ queryKey: adminsQueryKey })
    } catch (error) {
      handleServerError(
        error,
        isDisabled ? t('Failed to enable admin') : t('Failed to disable admin')
      )
    }
  }

  return (
    <div className='-ml-1.5 flex items-center gap-1'>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={() => openDialog('password')}
              aria-label={t('Reset Password')}
            />
          }
        >
          <KeyRound />
        </TooltipTrigger>
        <TooltipContent>{t('Reset Password')}</TooltipContent>
      </Tooltip>

      <DataTableRowActionMenu
        ariaLabel={t('Open menu')}
        contentClassName='w-48'
      >
        <DropdownMenuItem
          onClick={() => setResettingMfa(true)}
          disabled={isSelf || !admin.mfa_enabled}
        >
          {t('Reset Two-step Verification')}
          <DropdownMenuShortcut>
            <ShieldOff size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>

        <DropdownMenuItem onClick={handleToggleStatus} disabled={isSelf}>
          {isDisabled ? t('Enable') : t('Disable')}
          <DropdownMenuShortcut>
            {isDisabled ? <Power size={16} /> : <PowerOff size={16} />}
          </DropdownMenuShortcut>
        </DropdownMenuItem>

        <DropdownMenuSeparator />

        <DropdownMenuItem
          onClick={() => openDialog('delete')}
          className='text-destructive focus:text-destructive'
          disabled={isSelf}
        >
          {t('Delete')}
          <DropdownMenuShortcut>
            <Trash2 size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
      </DataTableRowActionMenu>

      <ConfirmDialog
        open={resettingMfa}
        onOpenChange={setResettingMfa}
        title={t('Reset two-step verification for "{{name}}"?', {
          name: admin.username,
        })}
        desc={t(
          'This removes the authenticator app, passkey and backup codes of this account and signs it out. API tokens keep working.'
        )}
        confirmText={t('Reset')}
        destructive
        isLoading={mfaBusy}
        handleConfirm={() => void handleResetMfa()}
      />
    </div>
  )
}
