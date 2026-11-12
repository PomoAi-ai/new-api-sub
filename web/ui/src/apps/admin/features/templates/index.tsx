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
// Modified 2026-11-05 by new-api-sub: channel templates page (from features/users/index.tsx; note alert from features/subscriptions/index.tsx); edited in the channel drawer
import { useQueryClient } from '@tanstack/react-query'
import { Info, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ChannelMutateDrawer } from '@/features/channels/components/drawers/channel-mutate-drawer'
import type { ChannelTemplateValues } from '@/features/channels/types'

import { createTemplate, templatesQueryKey, updateTemplate } from './api'
import { TemplatesDeleteDialog } from './components/templates-delete-dialog'
import {
  TemplatesProvider,
  useTemplates,
} from './components/templates-provider'
import { TemplatesTable } from './components/templates-table'

function TemplatesContent() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { open, setOpen, currentRow, setCurrentRow } = useTemplates()
  const editing = open === 'update' ? currentRow : null

  const saveTemplate = async (values: ChannelTemplateValues) => {
    if (editing) {
      await updateTemplate(editing.id, values)
    } else {
      await createTemplate(values)
    }
    toast.success(
      editing
        ? t('Template updated successfully')
        : t('Template created successfully')
    )
    void queryClient.invalidateQueries({ queryKey: templatesQueryKey })
  }

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('Templates')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div className='flex items-center gap-2'>
            <Alert variant='default' className='hidden px-3 py-2 sm:flex'>
              <Info className='h-4 w-4' />
              <AlertDescription className='text-xs'>
                {t(
                  'Templates apply automatically by channel type. Users cannot change them; sensitive settings are visible to super admins only.'
                )}
              </AlertDescription>
            </Alert>
            <Button
              size='sm'
              onClick={() => {
                setCurrentRow(null)
                setOpen('create')
              }}
            >
              <Plus className='h-4 w-4' />
              {t('Add Template')}
            </Button>
          </div>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <TemplatesTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <ChannelMutateDrawer
        variant='template'
        open={open === 'create' || open === 'update'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
        template={editing}
        onSaveTemplate={saveTemplate}
      />
      <TemplatesDeleteDialog />
    </>
  )
}

export function Templates() {
  return (
    <TemplatesProvider>
      <TemplatesContent />
    </TemplatesProvider>
  )
}
