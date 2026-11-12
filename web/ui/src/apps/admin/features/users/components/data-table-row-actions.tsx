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
// Modified 2026-11-06 by new-api-sub: portal user actions: reset password, copy account, reset and copy, log scope, settlement, reset two-step verification, enable/disable, delete
import { useQueryClient } from '@tanstack/react-query'
import type { Row } from '@tanstack/react-table'
import {
  Copy,
  KeyRound,
  Power,
  PowerOff,
  ReceiptText,
  RotateCcwKey,
  ShieldOff,
  Trash2,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTableRowActionMenu } from '@/components/data-table/core/row-action-menu'
import { Button } from '@/components/ui/button'
import {
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  USER_STATUS,
  resetUserMfa,
  setUserLogScope,
  setUserStatus,
  usersQueryKey,
  type PortalUser,
  type UserLogScope,
} from '../api'
import { useUsers } from './users-provider'

interface DataTableRowActionsProps {
  row: Row<PortalUser>
}

export function DataTableRowActions({ row }: DataTableRowActionsProps) {
  const { t } = useTranslation()
  const user = row.original
  const queryClient = useQueryClient()
  const { setOpen, setCurrentRow } = useUsers()
  const isDisabled = user.status === USER_STATUS.DISABLED
  const [resettingMfa, setResettingMfa] = useState(false)
  const [mfaBusy, setMfaBusy] = useState(false)

  // shortcut: read once per page load, so a URL set meanwhile needs a reload; refetch /api/me here if that bothers anyone.
  const portalUrl = useAuthStore((s) => s.auth.user?.portal_url)
  const { copyToClipboard } = useCopyToClipboard()

  const openDialog = (
    dialog: 'password' | 'delete' | 'reset-and-copy' | 'settlement'
  ) => {
    setCurrentRow(user)
    setOpen(dialog)
  }

  const portalUrlMissing = () => {
    if (portalUrl) return false
    toast.error(
      t(
        'The portal login URL is not set. Fill it in under System Settings first.'
      )
    )
    return true
  }

  const handleCopyAccount = () => {
    if (portalUrlMissing()) return
    void copyToClipboard(
      `${t('Login URL')}: ${portalUrl}\n${t('Username')}: ${user.username}`
    )
  }

  const handleResetAndCopy = () => {
    if (!portalUrlMissing()) openDialog('reset-and-copy')
  }

  const handleLogScope = async (logScope: UserLogScope) => {
    if (logScope === user.log_scope) return
    try {
      await setUserLogScope(user.id, logScope)
      toast.success(t('Log scope updated'))
      void queryClient.invalidateQueries({ queryKey: usersQueryKey })
    } catch (error) {
      handleServerError(error, t('Failed to update log scope'))
    }
  }

  const handleResetMfa = async () => {
    setMfaBusy(true)
    try {
      await resetUserMfa(user.id)
      toast.success(t('Two-step verification reset'))
      setResettingMfa(false)
      void queryClient.invalidateQueries({ queryKey: usersQueryKey })
    } catch (error) {
      handleServerError(error, t('Failed to reset two-step verification'))
    } finally {
      setMfaBusy(false)
    }
  }

  const handleToggleStatus = async () => {
    try {
      await setUserStatus(
        user.id,
        isDisabled ? USER_STATUS.ENABLED : USER_STATUS.DISABLED
      )
      toast.success(
        isDisabled
          ? t('User enabled successfully')
          : t('User disabled successfully')
      )
      void queryClient.invalidateQueries({ queryKey: usersQueryKey })
    } catch (error) {
      handleServerError(
        error,
        isDisabled ? t('Failed to enable user') : t('Failed to disable user')
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
        <DropdownMenuItem onClick={handleCopyAccount}>
          {t('Copy Account')}
          <DropdownMenuShortcut>
            <Copy size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>

        <DropdownMenuItem onClick={handleResetAndCopy}>
          {t('Reset and Copy')}
          <DropdownMenuShortcut>
            <RotateCcwKey size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>

        <DropdownMenuItem onClick={() => openDialog('settlement')}>
          {t('Settlement')}
          <DropdownMenuShortcut>
            <ReceiptText size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>

        <DropdownMenuSub>
          <DropdownMenuSubTrigger>{t('Log Scope')}</DropdownMenuSubTrigger>
          <DropdownMenuSubContent>
            <DropdownMenuRadioGroup
              value={user.log_scope}
              onValueChange={(value) =>
                void handleLogScope(value as UserLogScope)
              }
            >
              <DropdownMenuRadioItem value=''>
                {t('Follow global setting')}
              </DropdownMenuRadioItem>
              <DropdownMenuRadioItem value='all'>
                {t('All logs')}
              </DropdownMenuRadioItem>
              <DropdownMenuRadioItem value='self'>
                {t('Own channels only')}
              </DropdownMenuRadioItem>
            </DropdownMenuRadioGroup>
          </DropdownMenuSubContent>
        </DropdownMenuSub>

        <DropdownMenuItem
          onClick={() => setResettingMfa(true)}
          disabled={!user.mfa_enabled}
        >
          {t('Reset Two-step Verification')}
          <DropdownMenuShortcut>
            <ShieldOff size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>

        <DropdownMenuSeparator />

        <DropdownMenuItem onClick={handleToggleStatus}>
          {isDisabled ? t('Enable') : t('Disable')}
          <DropdownMenuShortcut>
            {isDisabled ? <Power size={16} /> : <PowerOff size={16} />}
          </DropdownMenuShortcut>
        </DropdownMenuItem>

        <DropdownMenuSeparator />

        <DropdownMenuItem
          onClick={() => openDialog('delete')}
          className='text-destructive focus:text-destructive'
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
          name: user.username,
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
