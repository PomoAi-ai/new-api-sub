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
// Modified 2026-11-04 by new-api-sub: delete channel template (from users/components/users-delete-dialog.tsx)
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { handleServerError } from '@/lib/handle-server-error'

import { deleteTemplate, templatesQueryKey } from '../api'
import { useTemplates } from './templates-provider'

export function TemplatesDeleteDialog() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { open, setOpen, currentRow } = useTemplates()
  const [isDeleting, setIsDeleting] = useState(false)

  const handleDelete = async () => {
    if (!currentRow) return

    setIsDeleting(true)
    try {
      await deleteTemplate(currentRow.id)
      toast.success(t('Template deleted successfully'))
      setOpen(null)
      void queryClient.invalidateQueries({ queryKey: templatesQueryKey })
    } catch (error) {
      handleServerError(error, t('Failed to delete template'))
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
          {t('This will permanently delete template')}{' '}
          <span className='font-semibold'>{currentRow?.name}</span>
          {t('. Existing channels are not affected.')}
        </>
      }
      confirmText={isDeleting ? t('Deleting...') : t('Delete')}
      destructive
      isLoading={isDeleting}
      handleConfirm={handleDelete}
    />
  )
}
