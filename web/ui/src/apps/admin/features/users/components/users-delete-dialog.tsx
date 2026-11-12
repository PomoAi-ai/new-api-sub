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
// Modified 2026-11-04 by new-api-sub: delete portal user; a 409 (user still owns channels) shows the server message
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { handleServerError } from '@/lib/handle-server-error'

import { adminsQueryKey } from '../../admins/api'
import { deleteUser, usersQueryKey } from '../api'
import { useUsers } from './users-provider'

export function UsersDeleteDialog() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { open, setOpen, currentRow } = useUsers()
  const [isDeleting, setIsDeleting] = useState(false)

  const handleDelete = async () => {
    if (!currentRow) return

    setIsDeleting(true)
    try {
      await deleteUser(currentRow.id)
      toast.success(t('User deleted successfully'))
      setOpen(null)
      void queryClient.invalidateQueries({ queryKey: usersQueryKey })
      void queryClient.invalidateQueries({ queryKey: adminsQueryKey })
    } catch (error) {
      handleServerError(error, t('Failed to delete user'))
    } finally {
      setIsDeleting(false)
    }
  }

  return (
    <ConfirmDialog
      open={open === 'delete'}
      onOpenChange={(open) => !open && setOpen(null)}
      title={t('Are you sure?')}
      desc={
        <>
          {t('This will permanently delete user')}{' '}
          <span className='font-semibold'>{currentRow?.username}</span>
          {t('. This action cannot be undone.')}
        </>
      }
      confirmText={isDeleting ? t('Deleting...') : t('Delete')}
      destructive
      isLoading={isDeleting}
      handleConfirm={handleDelete}
    />
  )
}
