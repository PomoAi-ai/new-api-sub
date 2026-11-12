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
// Modified 2026-11-05 by new-api-sub: portal users page (create, status, password reset, copy account, settlement, delete)
import { Plus, Zap } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'

import { resetUserPassword } from './api'
import { UserPasswordDialog } from './components/user-password-dialog'
import { UserQuickAddDialog } from './components/user-quick-add-dialog'
import { UserResetCopyDialog } from './components/user-reset-copy-dialog'
import { UserSettlementDialog } from './components/user-settlement-dialog'
import { UsersDeleteDialog } from './components/users-delete-dialog'
import { UsersMutateDrawer } from './components/users-mutate-drawer'
import { UsersProvider, useUsers } from './components/users-provider'
import { UsersTable } from './components/users-table'

function UsersContent() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, setCurrentRow } = useUsers()

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('Users')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div className='flex gap-2'>
            <Button
              size='sm'
              variant='outline'
              onClick={() => setOpen('quick-add')}
            >
              <Zap className='h-4 w-4' />
              {t('Quick Add')}
            </Button>
            <Button
              size='sm'
              onClick={() => {
                setCurrentRow(null)
                setOpen('create')
              }}
            >
              <Plus className='h-4 w-4' />
              {t('Add User')}
            </Button>
          </div>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <UsersTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <UsersMutateDrawer
        open={open === 'create'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
      />
      <UserQuickAddDialog
        open={open === 'quick-add'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
      />
      <UsersDeleteDialog />
      <UserPasswordDialog
        open={open === 'password'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
        account={currentRow}
        resetPassword={resetUserPassword}
      />
      <UserResetCopyDialog
        open={open === 'reset-and-copy'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
        user={currentRow}
      />
      <UserSettlementDialog
        open={open === 'settlement'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
        user={currentRow}
      />
    </>
  )
}

export function Users() {
  return (
    <UsersProvider>
      <UsersContent />
    </UsersProvider>
  )
}
