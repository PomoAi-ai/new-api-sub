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
// Modified 2026-11-04 by new-api-sub: admins page, super only (from features/users/index.tsx)
import { Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'

import { UserPasswordDialog } from '../users/components/user-password-dialog'
import { resetAdminPassword } from './api'
import { AdminsDeleteDialog } from './components/admins-delete-dialog'
import { AdminsMutateDrawer } from './components/admins-mutate-drawer'
import { AdminsProvider, useAdmins } from './components/admins-provider'
import { AdminsTable } from './components/admins-table'

function AdminsContent() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, setCurrentRow } = useAdmins()

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('Admins')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div className='flex gap-2'>
            <Button
              size='sm'
              onClick={() => {
                setCurrentRow(null)
                setOpen('create')
              }}
            >
              <Plus className='h-4 w-4' />
              {t('Add Admin')}
            </Button>
          </div>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <AdminsTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <AdminsMutateDrawer
        open={open === 'create'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
      />
      <AdminsDeleteDialog />
      <UserPasswordDialog
        open={open === 'password'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
        account={currentRow}
        resetPassword={resetAdminPassword}
      />
    </>
  )
}

export function Admins() {
  return (
    <AdminsProvider>
      <AdminsContent />
    </AdminsProvider>
  )
}
